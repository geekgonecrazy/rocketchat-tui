package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/geekgonecrazy/rocketchat-tui/internal/rocket"
)

// SaveCommands replaces the cached slash command list.
//
// It replaces rather than merges, like the member cache and for the same
// reason: the list is what the server offers now, so a command removed with the
// app that provided it should stop being offered here too.
func (s *Store) SaveCommands(commands []rocket.Command) error {
	return s.withTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM commands`); err != nil {
			return fmt.Errorf("store: clear commands: %w", err)
		}
		stmt, err := tx.Prepare(`
			INSERT INTO commands (name, params, description, client_only, provides_preview)
			VALUES (?,?,?,?,?)
			ON CONFLICT(name) DO UPDATE SET
				params           = excluded.params,
				description      = excluded.description,
				client_only      = excluded.client_only,
				provides_preview = excluded.provides_preview`)
		if err != nil {
			return fmt.Errorf("store: prepare command insert: %w", err)
		}
		defer stmt.Close()

		for _, command := range commands {
			name := strings.ToLower(strings.TrimSpace(command.Command))
			if name == "" {
				continue
			}
			if _, err := stmt.Exec(name, command.Params, command.Description,
				boolToInt(command.ClientOnly), boolToInt(command.ProvidesPreview)); err != nil {
				return fmt.Errorf("store: save command %s: %w", name, err)
			}
		}
		return nil
	})
}

// Commands returns the cached slash command list, alphabetically.
func (s *Store) Commands() ([]rocket.Command, error) {
	rows, err := s.db.Query(`
		SELECT name, params, description, client_only, provides_preview
		FROM commands ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: commands: %w", err)
	}
	defer rows.Close()

	var commands []rocket.Command
	for rows.Next() {
		var (
			command                rocket.Command
			clientOnly, hasPreview int
		)
		if err := rows.Scan(&command.Command, &command.Params, &command.Description,
			&clientOnly, &hasPreview); err != nil {
			return nil, fmt.Errorf("store: scan command: %w", err)
		}
		command.ClientOnly = clientOnly != 0
		command.ProvidesPreview = hasPreview != 0
		commands = append(commands, command)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: commands: %w", err)
	}
	return commands, nil
}
