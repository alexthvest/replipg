package character

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
)

func activeRuleSnapshot() contract.Snapshot {
	return contract.Snapshot{ID: "character", UserID: 1, MaxHealth: 100, Health: 70, Attack: 10, Defense: 5, State: contract.StateActive, Revision: 1}
}

func ruleConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	return cfg
}

func TestHealthRulesBoundedArithmetic(t *testing.T) {
	cfg := ruleConfig(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name                     string
		kind                     contract.HealthKind
		amount, health, revision int64
		changed                  bool
	}{
		{"zero damage", contract.HealthDamage, 0, 70, 1, false}, {"damage", contract.HealthDamage, 20, 50, 2, true},
		{"heal", contract.HealthHeal, 20, 90, 2, true}, {"excessive heal", contract.HealthHeal, math.MaxInt64, 100, 2, true},
		{"zero heal", contract.HealthHeal, 0, 70, 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, recovery, changed, err := applyHealthRule(activeRuleSnapshot(), 0, tt.kind, tt.amount, now, cfg)
			if err != nil || got.Health != tt.health || got.Revision != tt.revision || changed != tt.changed || recovery != 0 || !got.RecoverAt.IsZero() {
				t.Fatalf("unexpected result: %+v %d %v %v", got, recovery, changed, err)
			}
		})
	}

	snapshot := activeRuleSnapshot()
	snapshot.MaxHealth = math.MaxInt64
	snapshot.Health = math.MaxInt64 - 1
	got, _, _, err := applyHealthRule(snapshot, 0, contract.HealthHeal, math.MaxInt64, now, cfg)
	if err != nil || got.Health != math.MaxInt64 {
		t.Fatalf("overflowed healing: %+v %v", got, err)
	}

	snapshot.Health = snapshot.MaxHealth
	snapshot.Revision = math.MaxInt64
	got, _, changed, err := applyHealthRule(snapshot, 0, contract.HealthHeal, math.MaxInt64, now, cfg)
	if err != nil || changed || got != snapshot {
		t.Fatal("full-health healing must preserve revision, including max revision")
	}
}

func TestDefeatSnapshotsExactRecoveryAndDeadline(t *testing.T) {
	now := time.Date(2026, 1, 1, 3, 0, 0, 123456789, time.FixedZone("offset", 3*3600))
	for _, tt := range []struct {
		max     int64
		percent string
		want    int64
	}{
		{1, "25", 1}, {3, "25", 1}, {5, "25", 2}, {100, "25.00000000000000000001", 26},
		{math.MaxInt64, "50", 4611686018427387904}, {math.MaxInt64, "99.999999999999999999999999999", math.MaxInt64}, {math.MaxInt64, "0.00000000000000000000000001", 1},
	} {
		t.Run(tt.percent, func(t *testing.T) {
			cfg, err := ParseConfig(configFixture(t, "recovery_percent", tt.percent))
			if err != nil {
				t.Fatal(err)
			}

			snapshot := activeRuleSnapshot()
			snapshot.MaxHealth = tt.max
			snapshot.Health = tt.max
			got, hp, changed, err := applyHealthRule(snapshot, 0, contract.HealthDamage, math.MaxInt64, now, cfg)
			if err != nil || !changed || got.Health != 0 || got.State != contract.StateIncapacitated || hp != tt.want || got.Revision != 2 {
				t.Fatalf("unexpected defeat: %+v hp=%d %v", got, hp, err)
			}

			expected := now.Add(5 * time.Minute).UTC().Truncate(time.Millisecond).Add(time.Millisecond)

			if got.RecoverAt != expected || got.RecoverAt.Location() != time.UTC {
				t.Fatalf("deadline %v want %v", got.RecoverAt, expected)
			}

			recovered, remaining, changed, err := normalizeRecovery(got, hp, expected)
			if err != nil || !changed || recovered.Health != tt.want || recovered.State != contract.StateActive || remaining != 0 || !recovered.RecoverAt.IsZero() || recovered.Revision != 3 {
				t.Fatalf("recovery %+v %v", recovered, err)
			}
		})
	}
}

func TestRecoveryBoundariesAndNoPassiveRegeneration(t *testing.T) {
	cfg := ruleConfig(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	defeated, hp, _, err := applyHealthRule(activeRuleSnapshot(), 0, contract.HealthDamage, math.MaxInt64, now, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if defeated.RecoverAt != now.Add(5*time.Minute) {
		t.Fatal("exact milliseconds should not round an extra millisecond")
	}

	before := defeated.RecoverAt.Add(-time.Nanosecond)
	got, remaining, changed, err := normalizeRecovery(defeated, hp, before)
	if err != nil || changed || got != defeated || remaining != hp {
		t.Fatal("recovered before deadline")
	}

	for _, kind := range []contract.HealthKind{contract.HealthDamage, contract.HealthHeal} {
		_, _, _, err = applyHealthRule(defeated, hp, kind, 0, before, cfg)
		if !errors.Is(err, contract.ErrIncapacitated) {
			t.Fatalf("bypassed incapacitation: %v", err)
		}
	}

	got, remaining, changed, err = normalizeRecovery(defeated, hp, defeated.RecoverAt.Add(time.Hour))
	if err != nil || !changed || got.Health != 25 || remaining != 0 {
		t.Fatalf("expired recovery: %+v %v", got, err)
	}

	damaged, _, _, err := applyHealthRule(got, 0, contract.HealthDamage, 10, defeated.RecoverAt, cfg)
	if err != nil {
		t.Fatal(err)
	}

	again, _, changed, err := normalizeRecovery(damaged, 0, defeated.RecoverAt.Add(24*time.Hour))
	if err != nil || changed || again != damaged {
		t.Fatal("repeated recovery or passive regeneration")
	}
}

func TestRuleFailuresLeaveInputUnchanged(t *testing.T) {
	cfg := ruleConfig(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := activeRuleSnapshot()
	for _, tt := range []struct {
		name     string
		snapshot contract.Snapshot
		cfg      Config
		kind     contract.HealthKind
		amount   int64
		now      time.Time
		want     error
	}{
		{"negative", snapshot, cfg, contract.HealthDamage, -1, now, contract.ErrInvalidInput},
		{"kind", snapshot, cfg, "set", 1, now, contract.ErrInvalidInput},
		{"invalid config", snapshot, Config{}, contract.HealthDamage, 100, now, contract.ErrInvalidConfig},
		{"time overflow", snapshot, cfg, contract.HealthDamage, 100, time.UnixMilli(math.MaxInt64), contract.ErrExhausted},
		{"unrepresentable now", snapshot, cfg, contract.HealthDamage, 100, time.Unix(math.MaxInt64/1000+1, 0), contract.ErrExhausted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, hp, changed, err := applyHealthRule(tt.snapshot, 0, tt.kind, tt.amount, tt.now, tt.cfg)
			if !errors.Is(err, tt.want) || got != tt.snapshot || hp != 0 || changed {
				t.Fatalf("partial failure: %+v %d %v %v", got, hp, changed, err)
			}
		})
	}

	snapshot.Revision = math.MaxInt64
	got, _, changed, err := applyHealthRule(snapshot, 0, contract.HealthDamage, 1, now, cfg)
	if !errors.Is(err, contract.ErrExhausted) || got != snapshot || changed {
		t.Fatal("revision overflow changed state")
	}

	defeated := snapshot
	defeated.Health = 0
	defeated.State = contract.StateIncapacitated
	defeated.RecoverAt = now
	got, hp, changed, err := normalizeRecovery(defeated, 25, now)
	if !errors.Is(err, contract.ErrExhausted) || got != defeated || hp != 25 || changed {
		t.Fatal("recovery revision overflow changed state")
	}
}

func TestRulesTinyDurationAndStorageTimeBounds(t *testing.T) {
	cfg, err := ParseConfig(configFixture(t, "incapacitation_duration", "1ns"))
	if err != nil {
		t.Fatal(err)
	}

	for _, now := range []time.Time{time.UnixMilli(-1).Add(time.Nanosecond), time.UnixMilli(0), time.UnixMilli(math.MaxInt64 - 1), time.UnixMilli(math.MinInt64)} {
		got, _, _, err := applyHealthRule(activeRuleSnapshot(), 0, contract.HealthDamage, 100, now, cfg)
		if err != nil {
			t.Fatalf("representable deadline from %v: %v", now, err)
		}

		if !got.RecoverAt.Equal(time.UnixMilli(now.UnixMilli() + 1)) {
			t.Fatalf("deadline rounded incorrectly: %v from %v", got.RecoverAt, now)
		}
	}

	_, _, _, err = applyHealthRule(activeRuleSnapshot(), 0, contract.HealthDamage, 100, time.UnixMilli(math.MaxInt64), cfg)
	if !errors.Is(err, contract.ErrExhausted) {
		t.Fatalf("ceil overflow accepted: %v", err)
	}

	for _, now := range []time.Time{time.UnixMilli(math.MinInt64).Add(-time.Nanosecond), time.UnixMilli(math.MaxInt64).Add(time.Millisecond)} {
		_, _, _, err = normalizeRecovery(activeRuleSnapshot(), 0, now)
		if !errors.Is(err, contract.ErrExhausted) {
			t.Fatalf("unrepresentable clock accepted: %v", err)
		}
	}
}

func TestRulesRejectInvalidStoredStates(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*contract.Snapshot)
		hp     int64
	}{
		{"negative health", func(s *contract.Snapshot) { s.Health = -1 }, 0},
		{"above max", func(s *contract.Snapshot) { s.Health = 101 }, 0},
		{"zero max", func(s *contract.Snapshot) { s.MaxHealth = 0 }, 0},
		{"negative attack", func(s *contract.Snapshot) { s.Attack = -1 }, 0},
		{"negative defense", func(s *contract.Snapshot) { s.Defense = -1 }, 0},
		{"zero revision", func(s *contract.Snapshot) { s.Revision = 0 }, 0},
		{"unknown state", func(s *contract.Snapshot) { s.State = "unknown" }, 0},
		{"active zero health", func(s *contract.Snapshot) { s.Health = 0 }, 0},
		{"active deadline", func(s *contract.Snapshot) { s.RecoverAt = now }, 0},
		{"active recovery amount", func(s *contract.Snapshot) {}, 25},
		{"missing deadline", func(s *contract.Snapshot) { s.State = contract.StateIncapacitated; s.Health = 0 }, 25},
		{"missing recovery amount", func(s *contract.Snapshot) { s.State = contract.StateIncapacitated; s.Health = 0; s.RecoverAt = now }, 0},
		{"excess recovery amount", func(s *contract.Snapshot) { s.State = contract.StateIncapacitated; s.Health = 0; s.RecoverAt = now }, 101},
		{"fractional storage deadline", func(s *contract.Snapshot) {
			s.State = contract.StateIncapacitated
			s.Health = 0
			s.RecoverAt = now.Add(time.Nanosecond)
		}, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := activeRuleSnapshot()
			tt.mutate(&snapshot)
			got, hp, changed, err := normalizeRecovery(snapshot, tt.hp, now)
			if !errors.Is(err, contract.ErrInvalidInput) || got != snapshot || hp != tt.hp || changed {
				t.Fatalf("invalid state changed: %+v %v", got, err)
			}
		})
	}
}
