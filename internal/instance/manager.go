package instance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/juushimatsu/olcrtc-panel-lite/internal/model"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/security"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/store"
	"github.com/juushimatsu/olcrtc-panel-lite/internal/systemd"
)

// Manager coordinates SQLite, filesystem state and fixed systemd units.
type Manager struct {
	store        *store.Store
	secrets      *security.Secrets
	systemd      systemd.Controller
	instancesDir string
	runtimeDir   string
	maxInstances int
}

// SetMaxInstances applies a validated runtime limit.
func (m *Manager) SetMaxInstances(value int) {
	if value >= 1 && value <= 1000 {
		m.maxInstances = value
	}
}

// NewManager creates an instance manager.
func NewManager(st *store.Store, secrets *security.Secrets, controller systemd.Controller, instancesDir, runtimeDir string, maxInstances int) *Manager {
	return &Manager{store: st, secrets: secrets, systemd: controller, instancesDir: instancesDir, runtimeDir: runtimeDir, maxInstances: maxInstances}
}

// Create persists a stopped instance and writes its key and official YAML atomically.
func (m *Manager) Create(ctx context.Context, item model.Instance) (model.Instance, error) {
	ApplyDefaults(&item)
	if err := Validate(item); err != nil {
		return model.Instance{}, err
	}
	if item.Provider == "wbstream" && item.AuthToken == "" {
		if encrypted, _, err := m.store.Setting(ctx, "wb_token"); err == nil {
			item.AuthToken, _ = m.secrets.Decrypt(encrypted)
		}
	}
	count, err := m.store.InstanceCount(ctx)
	if err != nil {
		return model.Instance{}, err
	}
	if count >= m.maxInstances {
		return model.Instance{}, errors.New("instance limit reached")
	}
	item, err = m.encryptSecrets(item)
	if err != nil {
		return model.Instance{}, err
	}
	created, err := m.store.CreateInstance(ctx, item)
	if err != nil {
		return model.Instance{}, err
	}
	key, err := security.RandomHex(32)
	if err == nil {
		err = m.writeInstanceFiles(created, key)
	}
	if err != nil {
		_ = m.store.DeleteInstance(ctx, created.ID)
		_ = os.RemoveAll(m.instancePath(created.ID))
		_ = os.RemoveAll(m.runtimePath(created.ID))
		return model.Instance{}, err
	}
	return m.decorate(ctx, created)
}

// Update atomically changes configuration and rolls back after a failed restart.
func (m *Manager) Update(ctx context.Context, item model.Instance, clearAuth, clearProxy bool) (model.Instance, error) {
	old, err := m.store.Instance(ctx, item.ID)
	if err != nil {
		return model.Instance{}, err
	}
	ApplyDefaults(&item)
	if item.AuthToken == "" && !clearAuth {
		item.AuthToken = old.AuthToken
	} else if clearAuth {
		item.AuthToken = ""
	} else {
		item.AuthToken, err = m.secrets.Encrypt(item.AuthToken)
		if err != nil {
			return model.Instance{}, err
		}
	}
	if item.OutboundProxy == "" && !clearProxy {
		item.OutboundProxy = old.OutboundProxy
	} else if clearProxy {
		item.OutboundProxy = ""
	} else {
		item.OutboundProxy, err = m.secrets.Encrypt(item.OutboundProxy)
		if err != nil {
			return model.Instance{}, err
		}
	}
	plain, err := m.decryptSecrets(item)
	if err != nil {
		return model.Instance{}, err
	}
	if err := Validate(plain); err != nil {
		return model.Instance{}, err
	}
	oldConfig, _ := os.ReadFile(m.configPath(item.ID))
	key, err := m.readKey(item.ID)
	if err != nil {
		return model.Instance{}, err
	}
	if err := m.writeConfig(plain, key); err != nil {
		return model.Instance{}, err
	}
	updated, err := m.store.UpdateInstance(ctx, item)
	if err != nil {
		_ = atomicWrite(m.configPath(item.ID), oldConfig, 0o640)
		return model.Instance{}, err
	}
	status, _ := m.systemd.Status(ctx, item.ID)
	if status.State == "running" {
		restartErr := m.systemd.Restart(ctx, item.ID)
		if restartErr == nil {
			restartErr = systemd.WaitActive(ctx, m.systemd, item.ID, 20*time.Second)
		}
		if restartErr != nil {
			_, _ = m.store.UpdateInstance(ctx, old)
			_ = atomicWrite(m.configPath(item.ID), oldConfig, 0o640)
			_ = m.systemd.Restart(ctx, item.ID)
			return model.Instance{}, fmt.Errorf("new configuration failed, previous configuration restored: %w", restartErr)
		}
	}
	return m.decorate(ctx, updated)
}

// Delete follows the stop, disable, filesystem and database cleanup sequence.
func (m *Manager) Delete(ctx context.Context, id int64) error {
	if _, err := m.store.Instance(ctx, id); err != nil {
		return err
	}
	_ = m.systemd.Stop(ctx, id)
	_ = m.systemd.Disable(ctx, id)
	if err := os.RemoveAll(m.instancePath(id)); err != nil {
		return fmt.Errorf("remove instance config: %w", err)
	}
	if err := os.RemoveAll(m.runtimePath(id)); err != nil {
		return fmt.Errorf("remove instance runtime: %w", err)
	}
	return m.store.DeleteInstance(ctx, id)
}

// Duplicate creates a stopped copy with a new key and ID.
func (m *Manager) Duplicate(ctx context.Context, id int64) (model.Instance, error) {
	item, err := m.raw(ctx, id)
	if err != nil {
		return model.Instance{}, err
	}
	item.ID = 0
	item.Name += " - копия"
	item.CreatedAt = time.Time{}
	item.UpdatedAt = time.Time{}
	return m.Create(ctx, item)
}

// RotateKey replaces key.hex and restarts a running unit with rollback.
func (m *Manager) RotateKey(ctx context.Context, id int64) error {
	old, err := m.readKey(id)
	if err != nil {
		return err
	}
	key, err := security.RandomHex(32)
	if err != nil {
		return err
	}
	if err := atomicWrite(m.keyPath(id), []byte(key+"\n"), 0o640); err != nil {
		return err
	}
	m.applyOwnership(id)
	status, _ := m.systemd.Status(ctx, id)
	if status.State == "running" {
		restartErr := m.systemd.Restart(ctx, id)
		if restartErr == nil {
			restartErr = systemd.WaitActive(ctx, m.systemd, id, 20*time.Second)
		}
		if restartErr != nil {
			_ = atomicWrite(m.keyPath(id), []byte(old+"\n"), 0o640)
			m.applyOwnership(id)
			_ = m.systemd.Restart(ctx, id)
			return fmt.Errorf("key rotation failed and was rolled back: %w", restartErr)
		}
	}
	return nil
}

// List returns sanitized instances decorated with runtime status.
func (m *Manager) List(ctx context.Context) ([]model.Instance, error) {
	items, err := m.store.Instances(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i], _ = m.decorate(ctx, items[i])
	}
	return items, nil
}

// Get returns one sanitized decorated instance.
func (m *Manager) Get(ctx context.Context, id int64) (model.Instance, error) {
	item, err := m.store.Instance(ctx, id)
	if err != nil {
		return model.Instance{}, err
	}
	return m.decorate(ctx, item)
}

// Start starts an existing unit and waits for active state.
func (m *Manager) Start(ctx context.Context, id int64) error {
	if _, err := m.store.Instance(ctx, id); err != nil {
		return err
	}
	if err := m.systemd.Start(ctx, id); err != nil {
		return err
	}
	return systemd.WaitActive(ctx, m.systemd, id, 20*time.Second)
}

// Stop stops an existing unit.
func (m *Manager) Stop(ctx context.Context, id int64) error {
	if _, err := m.store.Instance(ctx, id); err != nil {
		return err
	}
	return m.systemd.Stop(ctx, id)
}

// Restart restarts an existing unit and waits for active state.
func (m *Manager) Restart(ctx context.Context, id int64) error {
	if _, err := m.store.Instance(ctx, id); err != nil {
		return err
	}
	if err := m.systemd.Restart(ctx, id); err != nil {
		return err
	}
	return systemd.WaitActive(ctx, m.systemd, id, 20*time.Second)
}

// Logs returns bounded service logs.
func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if _, err := m.store.Instance(ctx, id); err != nil {
		return "", err
	}
	return m.systemd.Logs(ctx, id, lines)
}

// URI renders an explicit secret-bearing URI operation.
func (m *Manager) URI(ctx context.Context, id int64, format string) (string, error) {
	item, err := m.raw(ctx, id)
	if err != nil {
		return "", err
	}
	key, err := m.readKey(id)
	if err != nil {
		return "", err
	}
	if format == "exclave" {
		return ExclaveURI(item, key, item.Name)
	}
	if format != "standard" {
		return "", errors.New("unsupported URI format")
	}
	return StandardURI(item, key, item.Name)
}

// ChangeRoom updates only Room ID through the same atomic path.
func (m *Manager) ChangeRoom(ctx context.Context, id int64, room string) (model.Instance, error) {
	item, err := m.raw(ctx, id)
	if err != nil {
		return model.Instance{}, err
	}
	item.RoomID = room
	item.AuthToken = ""
	item.OutboundProxy = ""
	return m.Update(ctx, item, false, false)
}

// UpdateWBToken applies one server-only token to all WB instances sequentially.
func (m *Manager) UpdateWBToken(ctx context.Context, token string) map[string]any {
	items, err := m.store.Instances(ctx)
	result := map[string]any{"updated": []int64{}, "failed": map[int64]string{}}
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	updated := make([]int64, 0)
	failed := make(map[int64]string)
	for _, item := range items {
		if item.Provider != "wbstream" {
			continue
		}
		plain, decryptErr := m.decryptSecrets(item)
		if decryptErr != nil {
			failed[item.ID] = decryptErr.Error()
			continue
		}
		plain.AuthToken = token
		plain.OutboundProxy = ""
		if _, updateErr := m.Update(ctx, plain, false, false); updateErr != nil {
			failed[item.ID] = updateErr.Error()
			continue
		}
		updated = append(updated, item.ID)
	}
	result["updated"] = updated
	result["failed"] = failed
	return result
}

func (m *Manager) decorate(ctx context.Context, item model.Instance) (model.Instance, error) {
	item.AuthToken = ""
	item.OutboundProxy = ""
	status, err := m.systemd.Status(ctx, item.ID)
	if err != nil {
		item.Status = "unknown"
		return item, err
	}
	item.Status = status.State
	item.UptimeSeconds = status.UptimeSeconds
	item.NetworkIngressBytes = status.IngressBytes
	item.NetworkEgressBytes = status.EgressBytes
	return item, nil
}

func (m *Manager) raw(ctx context.Context, id int64) (model.Instance, error) {
	item, err := m.store.Instance(ctx, id)
	if err != nil {
		return model.Instance{}, err
	}
	return m.decryptSecrets(item)
}

func (m *Manager) encryptSecrets(item model.Instance) (model.Instance, error) {
	var err error
	item.AuthToken, err = m.secrets.Encrypt(item.AuthToken)
	if err != nil {
		return model.Instance{}, err
	}
	item.OutboundProxy, err = m.secrets.Encrypt(item.OutboundProxy)
	return item, err
}

func (m *Manager) decryptSecrets(item model.Instance) (model.Instance, error) {
	var err error
	item.AuthToken, err = m.secrets.Decrypt(item.AuthToken)
	if err != nil {
		return model.Instance{}, err
	}
	item.OutboundProxy, err = m.secrets.Decrypt(item.OutboundProxy)
	return item, err
}

func (m *Manager) writeInstanceFiles(item model.Instance, key string) error {
	if err := os.MkdirAll(m.instancePath(item.ID), 0o750); err != nil {
		return fmt.Errorf("create instance config directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(m.runtimePath(item.ID), "data"), 0o750); err != nil {
		return fmt.Errorf("create instance runtime directory: %w", err)
	}
	if err := atomicWrite(m.keyPath(item.ID), []byte(key+"\n"), 0o640); err != nil {
		return err
	}
	plain, err := m.decryptSecrets(item)
	if err != nil {
		return err
	}
	if err := m.writeConfig(plain, key); err != nil {
		return err
	}
	m.applyOwnership(item.ID)
	return nil
}

func (m *Manager) writeConfig(item model.Instance, _ string) error {
	b, err := RenderYAML(item, m.keyPath(item.ID), filepath.Join(m.runtimePath(item.ID), "data"))
	if err != nil {
		return err
	}
	if err := atomicWrite(m.configPath(item.ID), b, 0o640); err != nil {
		return err
	}
	m.applyOwnership(item.ID)
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".panel-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary file: %w", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(path)
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("atomic rename: %w", err)
		}
	}
	return nil
}

func (m *Manager) readKey(id int64) (string, error) {
	b, err := os.ReadFile(m.keyPath(id))
	if err != nil {
		return "", fmt.Errorf("read instance key: %w", err)
	}
	key := string(b)
	for len(key) > 0 && (key[len(key)-1] == '\n' || key[len(key)-1] == '\r') {
		key = key[:len(key)-1]
	}
	if len(key) != 64 {
		return "", errors.New("instance key has invalid size")
	}
	return key, nil
}

func (m *Manager) instancePath(id int64) string {
	return filepath.Join(m.instancesDir, strconv.FormatInt(id, 10))
}
func (m *Manager) runtimePath(id int64) string {
	return filepath.Join(m.runtimeDir, strconv.FormatInt(id, 10))
}
func (m *Manager) configPath(id int64) string {
	return filepath.Join(m.instancePath(id), "config.yaml")
}
func (m *Manager) keyPath(id int64) string { return filepath.Join(m.instancePath(id), "key.hex") }

func (m *Manager) applyOwnership(id int64) {
	account, err := user.Lookup("olcrtc")
	if err != nil {
		return
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil {
		return
	}
	_ = os.Chown(m.instancePath(id), 0, gid)
	_ = os.Chown(m.configPath(id), 0, gid)
	_ = os.Chown(m.keyPath(id), 0, gid)
	_ = os.Chown(m.runtimePath(id), uid, gid)
	_ = os.Chown(filepath.Join(m.runtimePath(id), "data"), uid, gid)
}

// Store exposes persistence to subscription resolution and traffic collection.
func (m *Manager) Store() *store.Store { return m.store }

// IsNotFound normalizes database lookup failures for HTTP handlers.
func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
