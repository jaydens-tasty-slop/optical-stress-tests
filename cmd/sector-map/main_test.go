package main

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

type readCall struct {
	base  int64
	count int64
}

type recordingReader struct {
	data      []byte
	badSector int64
	calls     []readCall
}

func (r *recordingReader) ReadAt(p []byte, offset int64) (int, error) {
	base := offset / sectorSize
	count := int64(len(p)) / sectorSize
	r.calls = append(r.calls, readCall{base: base, count: count})
	if r.badSector >= base && r.badSector < base+count {
		return 0, errors.New("simulated read error")
	}
	copy(p, r.data[offset:offset+int64(len(p))])
	return len(p), nil
}

func testImage(sectors int64) []byte {
	data := make([]byte, sectors*sectorSize)
	for i := range data {
		data[i] = byte(i % 251)
	}
	return data
}

func TestScanStartsAtRequestedSectorAndWraps(t *testing.T) {
	const sectors = int64(8)
	data := testImage(sectors)
	device := &recordingReader{data: data, badSector: -1}
	states := make([]byte, sectors)
	r := &report{}

	err := scan(device, bytes.NewReader(data), sectors, states, r, scanConfig{
		startSector:     5,
		sectorsPerChunk: 3,
	})
	if err != nil {
		t.Fatalf("scan returned an error: %v", err)
	}

	wantCalls := []readCall{{base: 5, count: 3}, {base: 0, count: 3}, {base: 3, count: 2}}
	if !reflect.DeepEqual(device.calls, wantCalls) {
		t.Fatalf("device calls = %#v, want %#v", device.calls, wantCalls)
	}
	if r.GoodSectors != sectors || r.BadSectors != 0 {
		t.Fatalf("good/bad sectors = %d/%d, want %d/0", r.GoodSectors, r.BadSectors, sectors)
	}
	for sector, state := range states {
		if state != 1 {
			t.Errorf("state[%d] = %d, want 1", sector, state)
		}
	}
}

func TestScanBisectsFailedChunk(t *testing.T) {
	const sectors = int64(4)
	data := testImage(sectors)
	device := &recordingReader{data: data, badSector: 2}
	states := make([]byte, sectors)
	r := &report{}

	err := scan(device, bytes.NewReader(data), sectors, states, r, scanConfig{
		sectorsPerChunk: sectors,
	})
	if err != nil {
		t.Fatalf("scan returned an error: %v", err)
	}

	wantCalls := []readCall{
		{base: 0, count: 4},
		{base: 0, count: 2},
		{base: 2, count: 2},
		{base: 2, count: 1},
		{base: 3, count: 1},
	}
	if !reflect.DeepEqual(device.calls, wantCalls) {
		t.Fatalf("device calls = %#v, want %#v", device.calls, wantCalls)
	}
	if r.GoodSectors != 3 || r.BadSectors != 1 {
		t.Fatalf("good/bad sectors = %d/%d, want 3/1", r.GoodSectors, r.BadSectors)
	}
	if r.ChunkReadErrors != 1 || r.RecoveryReadErrors != 2 || r.SectorReadErrors != 1 {
		t.Fatalf("read errors = chunk:%d recovery:%d sector:%d, want 1/2/1",
			r.ChunkReadErrors, r.RecoveryReadErrors, r.SectorReadErrors)
	}
	if r.DeviceReadCalls != 5 || r.RecoveryReadCalls != 4 {
		t.Fatalf("read calls = total:%d recovery:%d, want 5/4", r.DeviceReadCalls, r.RecoveryReadCalls)
	}
	wantStates := []byte{1, 1, 2, 1}
	if !bytes.Equal(states, wantStates) {
		t.Fatalf("states = %v, want %v", states, wantStates)
	}
}
