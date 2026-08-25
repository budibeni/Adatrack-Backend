package controllers

import (
	"database/sql"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// vehicleRegistry — (company_code, IMEI) → vehicle (id, model, plate) cache
// dipakai bridge NATS→hub. Multi-tenant safe: seluruh lookup di-route ke
// adatrack_gps_{company_code} milik device. Anti-leak: hanya vehicle terdaftar
// yang di-push ke WebSocket. Cache TTL pendek dengan fallback negatif agar
// IMEI tak dikenal tidak menghantam DB.
// ---------------------------------------------------------------------------

type vehicleInfo struct {
	ID    uint64
	Model string
	Plate string
}

type registryEntry struct {
	info   vehicleInfo
	expire time.Time
}

type vehicleRegistry struct {
	mu    sync.Mutex
	cache map[string]registryEntry
	ttl   time.Duration
}

func newVehicleRegistry() *vehicleRegistry {
	return &vehicleRegistry{
		cache: make(map[string]registryEntry),
		ttl:   5 * time.Minute,
	}
}

// cacheKey scopes the registry entry per tenant (ISO company_code + imei).
func registryKey(companyCode, imei string) string {
	return companyCode + ":" + imei
}

// lookup returns the vehicle info for a (company, imei) pair.
func (r *vehicleRegistry) lookup(companyCode, imei string) (vehicleInfo, bool) {
	key := registryKey(companyCode, imei)
	now := time.Now()

	r.mu.Lock()
	if e, ok := r.cache[key]; ok && now.Before(e.expire) {
		r.mu.Unlock()
		return e.info, true
	}
	r.mu.Unlock()

	info, err := r.fetch(companyCode, imei)
	if err != nil || info.ID == 0 {
		// Cache negatif singkat agar IMEI tak dikenal tidak membanjiri DB.
		r.mu.Lock()
		r.cache[key] = registryEntry{
			info:   vehicleInfo{}, // ID 0 => not found
			expire: time.Now().Add(30 * time.Second),
		}
		r.mu.Unlock()
		return vehicleInfo{}, false
	}

	r.mu.Lock()
	r.cache[key] = registryEntry{info: info, expire: time.Now().Add(r.ttl)}
	r.mu.Unlock()
	return info, true
}

// fetch queries the vehicles table (of the A company DB) for a registered,
// non-deleted device. READ path (B4 HA read/write split): replica-preferred
// dengan fallback primary otomatis — data vehicle jarang berubah sehingga
// aman terhadap replication lag singkat.
func (r *vehicleRegistry) fetch(companyCode, imei string) (vehicleInfo, error) {
	db, err := companyReadByCode(companyCode)
	if err != nil || db == nil {
		return vehicleInfo{}, err
	}
	var info vehicleInfo
	var model, plate sql.NullString
	err = db.QueryRow(
		`SELECT id, device_model, plate_number FROM vehicles WHERE imei = ? AND deleted_at IS NULL LIMIT 1`,
		imei,
	).Scan(&info.ID, &model, &plate)
	if err != nil {
		return vehicleInfo{}, err
	}
	if model.Valid {
		info.Model = model.String
	}
	if plate.Valid {
		info.Plate = plate.String
	}
	return info, nil
}

// invalidate clears a cached (company, IMEI) entry (used by tests / config changes).
func (r *vehicleRegistry) invalidate(companyCode, imei string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cache, registryKey(companyCode, imei))
}
