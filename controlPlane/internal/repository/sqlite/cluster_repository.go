package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/repository"
)

// ErrClusterNotFound is returned when a cluster is not found in the database.
var ErrClusterNotFound = errors.New("cluster not found")

// clusterRepository is the SQLite-backed implementation of repository.ClusterRepository.
type clusterRepository struct {
	db *sql.DB
}

// NewClusterRepository returns a clusterRepository backed by the given SQLite
// database connection. The caller is responsible for opening and closing the
// database.
func NewClusterRepository(db *sql.DB) *clusterRepository {
	return &clusterRepository{db: db}
}

// Create persists a new cluster definition.
func (r *clusterRepository) Create(ctx context.Context, cluster *domain.Cluster) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clusters (
			id, name, state, kubernetes_version, control_plane_node,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		cluster.ID,
		cluster.Spec.Name,
		string(cluster.Status.State),
		cluster.Status.KubernetesVersion,
		cluster.Status.ControlPlaneNode,
		formatTime(cluster.CreatedAt),
		formatTime(cluster.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("creating cluster %q: %w", cluster.Spec.Name, err)
	}
	return nil
}

// GetByID returns a single cluster by its UUID.
func (r *clusterRepository) GetByID(ctx context.Context, id string) (*domain.Cluster, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, state, kubernetes_version, control_plane_node,
		       created_at, updated_at
		FROM clusters WHERE id = ?`, id)

	var c domain.Cluster
	var createdAt, updatedAt string
	err := row.Scan(
		&c.ID,
		&c.Spec.Name,
		&c.Status.State,
		&c.Status.KubernetesVersion,
		&c.Status.ControlPlaneNode,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying cluster %q: %w", id, err)
	}

	var err1 error
	c.CreatedAt, err1 = parseTime(createdAt)
	if err1 != nil {
		return nil, fmt.Errorf("parsing created_at %q: %w", createdAt, err1)
	}
	c.UpdatedAt, err1 = parseTime(updatedAt)
	if err1 != nil {
		return nil, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err1)
	}

	return &c, nil
}

// GetByName returns a single cluster by its unique name.
func (r *clusterRepository) GetByName(ctx context.Context, name string) (*domain.Cluster, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, state, kubernetes_version, control_plane_node,
		       created_at, updated_at
		FROM clusters WHERE name = ?`, name)

	var c domain.Cluster
	var createdAt, updatedAt string
	err := row.Scan(
		&c.ID,
		&c.Spec.Name,
		&c.Status.State,
		&c.Status.KubernetesVersion,
		&c.Status.ControlPlaneNode,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying cluster %q: %w", name, err)
	}

	var err1 error
	c.CreatedAt, err1 = parseTime(createdAt)
	if err1 != nil {
		return nil, fmt.Errorf("parsing created_at %q: %w", createdAt, err1)
	}
	c.UpdatedAt, err1 = parseTime(updatedAt)
	if err1 != nil {
		return nil, fmt.Errorf("parsing updated_at %q: %w", updatedAt, err1)
	}

	return &c, nil
}

// GetAll returns every registered cluster.
func (r *clusterRepository) GetAll(ctx context.Context) ([]*domain.Cluster, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, state, kubernetes_version, control_plane_node,
		       created_at, updated_at
		FROM clusters ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("querying clusters: %w", err)
	}
	defer rows.Close()

	var clusters []*domain.Cluster
	for rows.Next() {
		var c domain.Cluster
		var createdAt, updatedAt string
		err := rows.Scan(
			&c.ID,
			&c.Spec.Name,
			&c.Status.State,
			&c.Status.KubernetesVersion,
			&c.Status.ControlPlaneNode,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning cluster row: %w", err)
		}

		var err1 error
		c.CreatedAt, err1 = parseTime(createdAt)
		if err1 != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err1)
		}
		c.UpdatedAt, err1 = parseTime(updatedAt)
		if err1 != nil {
			return nil, fmt.Errorf("parsing updated_at: %w", err1)
		}
		clusters = append(clusters, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating clusters: %w", err)
	}
	return clusters, nil
}

// Update persists changes to an existing cluster.
func (r *clusterRepository) Update(ctx context.Context, cluster *domain.Cluster) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE clusters SET
			name = ?, state = ?, kubernetes_version = ?, control_plane_node = ?,
			updated_at = ?
		WHERE id = ?`,
		cluster.Spec.Name,
		string(cluster.Status.State),
		cluster.Status.KubernetesVersion,
		cluster.Status.ControlPlaneNode,
		formatTime(cluster.UpdatedAt),
		cluster.ID,
	)
	if err != nil {
		return fmt.Errorf("updating cluster %q: %w", cluster.Spec.Name, err)
	}
	return nil
}

// Delete removes a cluster by its UUID.
func (r *clusterRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM clusters WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting cluster %q: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return repository.ErrNotFound
	}
	return nil
}

// Ensure _ is compiled for the repository interface.
var _ repository.ClusterRepository = (*clusterRepository)(nil)
