package settings

import (
	"testing"

	"rbi-go/internal/db"
)

// TestVideoEncoderStore_GetMode_FreshDB_ReturnsAuto verifies that GetMode on a fresh DB returns Auto (the default).
func TestVideoEncoderStore_GetMode_FreshDB_ReturnsAuto(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewVideoEncoderStore(database.Unwrap())
	mode, err := store.GetMode()
	if err != nil {
		t.Fatalf("GetMode failed: %v", err)
	}

	if mode != db.VideoEncoderModeAuto {
		t.Errorf("expected VideoEncoderModeAuto, got %v", mode)
	}
}

// TestVideoEncoderStore_SetGet_RoundTrip_Auto verifies that SetMode(Auto) and GetMode return the same value.
func TestVideoEncoderStore_SetGet_RoundTrip_Auto(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewVideoEncoderStore(database.Unwrap())
	if err := store.SetMode(db.VideoEncoderModeAuto); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	mode, err := store.GetMode()
	if err != nil {
		t.Fatalf("GetMode failed: %v", err)
	}

	if mode != db.VideoEncoderModeAuto {
		t.Errorf("expected VideoEncoderModeAuto, got %v", mode)
	}
}

// TestVideoEncoderStore_SetGet_RoundTrip_Cpu verifies that SetMode(Cpu) and GetMode return the same value.
func TestVideoEncoderStore_SetGet_RoundTrip_Cpu(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewVideoEncoderStore(database.Unwrap())
	if err := store.SetMode(db.VideoEncoderModeCpu); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	mode, err := store.GetMode()
	if err != nil {
		t.Fatalf("GetMode failed: %v", err)
	}

	if mode != db.VideoEncoderModeCpu {
		t.Errorf("expected VideoEncoderModeCpu, got %v", mode)
	}
}

// TestVideoEncoderStore_SetGet_RoundTrip_Gpu verifies that SetMode(Gpu) and GetMode return the same value.
func TestVideoEncoderStore_SetGet_RoundTrip_Gpu(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewVideoEncoderStore(database.Unwrap())
	if err := store.SetMode(db.VideoEncoderModeGpu); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	mode, err := store.GetMode()
	if err != nil {
		t.Fatalf("GetMode failed: %v", err)
	}

	if mode != db.VideoEncoderModeGpu {
		t.Errorf("expected VideoEncoderModeGpu, got %v", mode)
	}
}

// TestVideoEncoderStore_SetMode_UpdatesCacheWithoutReload verifies that SetMode updates the in-memory cache.
func TestVideoEncoderStore_SetMode_UpdatesCacheWithoutReload(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewVideoEncoderStore(database.Unwrap())

	// Set to Cpu
	if err := store.SetMode(db.VideoEncoderModeCpu); err != nil {
		t.Fatalf("SetMode(Cpu) failed: %v", err)
	}

	// GetMode should return Cpu without reloading from DB
	mode, err := store.GetMode()
	if err != nil {
		t.Fatalf("GetMode failed: %v", err)
	}
	if mode != db.VideoEncoderModeCpu {
		t.Errorf("expected Cpu, got %v", mode)
	}

	// Update to Gpu
	if err := store.SetMode(db.VideoEncoderModeGpu); err != nil {
		t.Fatalf("SetMode(Gpu) failed: %v", err)
	}

	// GetMode should return Gpu (from cache)
	mode, err = store.GetMode()
	if err != nil {
		t.Fatalf("GetMode after SetMode failed: %v", err)
	}
	if mode != db.VideoEncoderModeGpu {
		t.Errorf("expected Gpu, got %v", mode)
	}
}

// TestVideoEncoderStore_SetMode_Overwrites_PreviousValue verifies that SetMode replaces a previous value.
func TestVideoEncoderStore_SetMode_Overwrites_PreviousValue(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	store := NewVideoEncoderStore(database.Unwrap())

	// Set to Cpu
	if err := store.SetMode(db.VideoEncoderModeCpu); err != nil {
		t.Fatalf("SetMode(Cpu) failed: %v", err)
	}

	// Overwrite with Gpu
	if err := store.SetMode(db.VideoEncoderModeGpu); err != nil {
		t.Fatalf("SetMode(Gpu) failed: %v", err)
	}

	// Verify the DB was updated (create a new store instance and check)
	store2 := NewVideoEncoderStore(database.Unwrap())
	mode, err := store2.GetMode()
	if err != nil {
		t.Fatalf("GetMode on new store failed: %v", err)
	}

	if mode != db.VideoEncoderModeGpu {
		t.Errorf("expected Gpu in new store, got %v", mode)
	}
}

// TestVideoEncoderStore_GetMode_ReadsPersistedValueAfterRestart verifies that a new store instance
// reads the persisted value from the DB.
func TestVideoEncoderStore_GetMode_ReadsPersistedValueAfterRestart(t *testing.T) {
	database, err := db.Connect(":memory:")
	if err != nil {
		t.Fatalf("failed to connect to DB: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// First store: set to Gpu
	store1 := NewVideoEncoderStore(database.Unwrap())
	if err := store1.SetMode(db.VideoEncoderModeGpu); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	// Second store (simulating restart): should read the persisted value
	store2 := NewVideoEncoderStore(database.Unwrap())
	mode, err := store2.GetMode()
	if err != nil {
		t.Fatalf("GetMode on new store failed: %v", err)
	}

	if mode != db.VideoEncoderModeGpu {
		t.Errorf("expected persisted Gpu, got %v", mode)
	}
}
