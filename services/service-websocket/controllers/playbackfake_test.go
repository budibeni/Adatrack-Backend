package controllers

import (
	"context"
	"strings"
	"sync"

	"adatrack_gps/internal/geo"
	"adatrack_gps/service-websocket/models"
)

// fakePlayback is an in-memory PlaybackStore (B7.4).
type fakePlayback struct {
	mu        sync.Mutex
	positions map[string][]models.Position
	calls     int
	err       error
}

func newFakePlayback() *fakePlayback {
	return &fakePlayback{positions: map[string][]models.Position{}}
}

// add registers a route for one vehicle.
func (f *fakePlayback) add(company string, vehicleID int64, positions ...models.Position) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := playbackKey(company, vehicleID)
	f.positions[key] = append(f.positions[key], positions...)
}

// callCount reports how many queries were served.
func (f *fakePlayback) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// VehiclePlayback implements PlaybackStore (the limit is honoured like the SQL
// LIMIT so truncation is testable).
func (f *fakePlayback) VehiclePlayback(_ context.Context, q PlaybackQuery) ([]models.Position, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	all := f.positions[playbackKey(q.CompanyCode, q.VehicleID)]
	if q.Limit > 0 && len(all) > q.Limit {
		return append([]models.Position{}, all[:q.Limit]...), nil
	}
	return append([]models.Position{}, all...), nil
}

// playbackKey scopes a route to its tenant + vehicle.
func playbackKey(company string, vehicleID int64) string {
	return strings.ToUpper(strings.TrimSpace(company)) + "|" + itoa(vehicleID)
}

// fakeRegions is an in-memory RegionStore (B7.3).
type fakeRegions struct {
	mu      sync.Mutex
	regions []geo.Region
	calls   int
	err     error
}

func newFakeRegions() *fakeRegions {
	return &fakeRegions{regions: []geo.Region{
		{Level: geo.LevelCity, Province: "DKI Jakarta", City: "Kota Jakarta Pusat",
			Point: geo.Point{Lat: -6.1805, Lon: 106.8284}},
		{Level: geo.LevelCity, Province: "Jawa Barat", City: "Kota Bandung",
			Point: geo.Point{Lat: -6.9175, Lon: 107.6191}},
		{Level: geo.LevelProvince, Province: "DKI Jakarta",
			Point: geo.Point{Lat: -6.1741, Lon: 106.8297}},
		{Level: geo.LevelProvince, Province: "Jawa Barat",
			Point: geo.Point{Lat: -6.9147, Lon: 107.6098}},
	}}
}

// set replaces the fixture rows (an empty slice exercises the fallback).
func (f *fakeRegions) set(regions ...geo.Region) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.regions = regions
}

// callCount reports how often the reference data was loaded (cache assertions).
func (f *fakeRegions) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// RegionCentroids implements RegionStore.
func (f *fakeRegions) RegionCentroids(_ context.Context) ([]geo.Region, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]geo.Region{}, f.regions...), nil
}
