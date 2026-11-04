package character

import (
	"math"
	"math/big"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
)

// normalizeRecovery uses the amount stored at defeat, never current tuning.
// Failed transitions return the original values so no partial result can persist.
func normalizeRecovery(snapshot contract.Snapshot, recoveryHealth int64, now time.Time) (contract.Snapshot, int64, bool, error) {
	if err := validateRuleState(snapshot, recoveryHealth); err != nil {
		return snapshot, recoveryHealth, false, err
	}
	if _, err := storageMilliseconds(now); err != nil {
		return snapshot, recoveryHealth, false, err
	}
	if snapshot.State == contract.StateActive || now.Before(snapshot.RecoverAt) {
		return snapshot, recoveryHealth, false, nil
	}
	if snapshot.Revision == math.MaxInt64 {
		return snapshot, recoveryHealth, false, ruleError(contract.ErrorExhausted, "revision")
	}
	snapshot.Health = recoveryHealth
	snapshot.State = contract.StateActive
	snapshot.RecoverAt = time.Time{}
	snapshot.Revision++
	return snapshot, 0, true, nil
}

// applyHealthRule requires the caller to normalize recovery first. It cannot
// bypass an incapacitation, including for zero-amount operations.
func applyHealthRule(snapshot contract.Snapshot, recoveryHealth int64, kind contract.HealthKind, amount int64, now time.Time, cfg Config) (contract.Snapshot, int64, bool, error) {
	if !kind.Valid() || amount < 0 {
		return snapshot, recoveryHealth, false, ruleError(contract.ErrorInvalidInput, "health_change")
	}
	if err := validateRuleState(snapshot, recoveryHealth); err != nil {
		return snapshot, recoveryHealth, false, err
	}
	if cfg.maxHealth <= 0 || cfg.attack < 0 || cfg.defense < 0 || cfg.incapacitationDuration <= 0 || !decimalPercent(cfg.recoveryPercent) {
		return snapshot, recoveryHealth, false, ruleError(contract.ErrorInvalidConfig, "config")
	}
	if _, err := storageMilliseconds(now); err != nil {
		return snapshot, recoveryHealth, false, err
	}
	if snapshot.State == contract.StateIncapacitated {
		return snapshot, recoveryHealth, false, ruleError(contract.ErrorIncapacitated, "state")
	}
	health := snapshot.Health
	switch kind {
	case contract.HealthDamage:
		health -= min(amount, health)
	case contract.HealthHeal:
		health += min(amount, snapshot.MaxHealth-health)
	}
	if health == snapshot.Health {
		return snapshot, recoveryHealth, false, nil
	}
	if snapshot.Revision == math.MaxInt64 {
		return snapshot, recoveryHealth, false, ruleError(contract.ErrorExhausted, "revision")
	}
	next := snapshot
	next.Health = health
	next.Revision++
	if health == 0 {
		deadline, err := recoveryDeadline(now, cfg.incapacitationDuration)
		if err != nil {
			return snapshot, recoveryHealth, false, err
		}
		next.State = contract.StateIncapacitated
		next.RecoverAt = deadline
		recoveryHealth = exactRecoveryHealth(snapshot.MaxHealth, cfg.recoveryPercent)
	}
	return next, recoveryHealth, true, nil
}

func exactRecoveryHealth(maxHealth int64, percent string) int64 {
	// The caller has already validated the decimal percentage. Rational and
	// integer intermediates prevent rounding loss and int64 multiplication overflow.
	ratio, _ := new(big.Rat).SetString(percent)
	numerator := new(big.Int).Mul(big.NewInt(maxHealth), ratio.Num())
	denominator := new(big.Int).Mul(big.NewInt(100), ratio.Denom())
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() > 0 {
		whole.Add(whole, big.NewInt(1))
	}
	return min(maxHealth, max(1, whole.Int64()))
}

func validateRuleState(snapshot contract.Snapshot, recoveryHealth int64) error {
	if snapshot.MaxHealth <= 0 || snapshot.Health < 0 || snapshot.Health > snapshot.MaxHealth || snapshot.Attack < 0 || snapshot.Defense < 0 || snapshot.Revision < 1 {
		return ruleError(contract.ErrorInvalidInput, "state")
	}
	switch snapshot.State {
	case contract.StateActive:
		if snapshot.Health == 0 || !snapshot.RecoverAt.IsZero() || recoveryHealth != 0 {
			return ruleError(contract.ErrorInvalidInput, "state")
		}
	case contract.StateIncapacitated:
		if snapshot.Health != 0 || snapshot.RecoverAt.IsZero() || recoveryHealth < 1 || recoveryHealth > snapshot.MaxHealth {
			return ruleError(contract.ErrorInvalidInput, "state")
		}
		if _, err := storageMilliseconds(snapshot.RecoverAt); err != nil {
			return err
		}
		if snapshot.RecoverAt.Nanosecond()%int(time.Millisecond) != 0 {
			return ruleError(contract.ErrorInvalidInput, "recover_at")
		}
	default:
		return ruleError(contract.ErrorInvalidInput, "state")
	}
	return nil
}

func ruleError(kind contract.ErrorKind, field string) error {
	return &contract.DomainError{Kind: kind, Field: field}
}

// storageMilliseconds validates before conversion: Time.UnixMilli silently
// overflows for times beyond the signed storage range.
func storageMilliseconds(value time.Time) (int64, error) {
	seconds := value.Unix()
	if !time.Unix(seconds, int64(value.Nanosecond())).Equal(value) {
		return 0, ruleError(contract.ErrorExhausted, "time")
	}
	milliseconds := new(big.Int).Mul(big.NewInt(seconds), big.NewInt(1000))
	milliseconds.Add(milliseconds, big.NewInt(int64(value.Nanosecond())/int64(time.Millisecond)))
	if !milliseconds.IsInt64() {
		return 0, ruleError(contract.ErrorExhausted, "time")
	}
	return milliseconds.Int64(), nil
}

func recoveryDeadline(now time.Time, duration time.Duration) (time.Time, error) {
	// Keep submillisecond precision until after adding the duration, then ceil.
	// This also avoids Time.Add saturation near the representable time limits.
	nanoseconds := new(big.Int).Mul(big.NewInt(now.Unix()), big.NewInt(int64(time.Second)))
	nanoseconds.Add(nanoseconds, big.NewInt(int64(now.Nanosecond())))
	nanoseconds.Add(nanoseconds, big.NewInt(int64(duration)))
	milliseconds, remainder := new(big.Int), new(big.Int)
	milliseconds.DivMod(nanoseconds, big.NewInt(int64(time.Millisecond)), remainder)
	if remainder.Sign() > 0 {
		milliseconds.Add(milliseconds, big.NewInt(1))
	}
	if !milliseconds.IsInt64() {
		return time.Time{}, ruleError(contract.ErrorExhausted, "recover_at")
	}
	deadline := time.UnixMilli(milliseconds.Int64()).UTC()
	if deadline.IsZero() || !deadline.After(now) {
		return time.Time{}, ruleError(contract.ErrorExhausted, "recover_at")
	}
	return deadline, nil
}
