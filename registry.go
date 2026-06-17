package raven

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
	"github.com/google/uuid"
)

/*
 within a single authorized_keys line you can prefix per-key restrictions -from="1.2.3.4" (lock to an IP),
 no-port-forwarding, etc. So you can clamp even one key.
*/

type registry struct {
	db *sql.DB
}

func (r *registry) initUser(o *owner) error {

	const query = `
		INSERT OR REPLACE INTO owner
		(id, owner_id)
		VALUES
		(?, ?)
	`

	o.Id = 1
	_, err := r.db.Exec(query, o.Id, o.OwnerId)
	if err != nil {
		return err
	}

	return nil
}

func (r *registry) getUser() (*tgInt, error) {

	const query = `
		SELECT 
		owner_id
		FROM owner
	`

	row := r.db.QueryRow(query)

	var ownerId tgInt

	err := row.Scan(&ownerId); 
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no user found")
	}
	
	if err != nil {
		return nil, err
	}

	return &ownerId, nil 
}

func (r *registry) addVm(m *machine) error {

	m.Id = uuid.New()
	m.CreatedAt = time.Now()

	const query = `
		INSERT INTO machines
		(id, name, description, created_at, host, port, ssh_user, key_path, host_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.Exec(query, m.Id, m.Name, m.Description, m.CreatedAt, m.Host, m.Port, m.SshUser, m.KeyPath, m.HostKey)
	if err != nil {
		return fmt.Errorf("insert machine %q: %w", m.Name, err)
	}

	return nil
}

func (r *registry) removeVm(name string) error {

	const query = `
		DELETE
		FROM machines
		WHERE name = ?
	`

	_, err := r.db.Exec(query, name)
	if err != nil {
		return fmt.Errorf("remove machine %q: %w", name, err)
	}

	return nil
}

func (r *registry) getVm(name string) (*machine, error) {

	const query = `
		SELECT
		id, name, description, created_at, host, port, ssh_user, key_path, host_key
		FROM machines
		WHERE name = ?
	`

	row := r.db.QueryRow(query, name)

	m := &machine{}
	err := row.Scan(&m.Id, &m.Name, &m.Description, &m.CreatedAt, &m.Host, &m.Port, &m.SshUser, &m.KeyPath, &m.HostKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no machine with name %s found", name)
	}
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (r *registry) listVm() ([]*machine, error) {

	const query = `
		SELECT
		id, name, description, created_at, host, port, ssh_user, key_path, host_key
		FROM machines
	`
	rows, err := r.db.Query(query)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var machines []*machine
	for rows.Next() {
		m := &machine{}
		err := rows.Scan(&m.Id, &m.Name, &m.Description, &m.CreatedAt, &m.Host, &m.Port, &m.SshUser, &m.KeyPath, &m.HostKey)
		if err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}

	if rows.Err() != nil {
		return nil, rows.Err()
	}

	return machines, nil
}

func (r *registry) updateVm(m *machine) error {

	const query = `
		UPDATE machines
		SET
		name=?, description=?, host=?, port=?, ssh_user=?, key_path=?, host_key=?
		WHERE id = ?
	`

	_, err := r.db.Exec(query, m.Name, m.Description, m.Host, m.Port, m.SshUser, m.KeyPath, m.HostKey, m.Id)
	if err != nil {
		return fmt.Errorf("update machine %q: %w", m.Name, err)
	}

	return nil
}
