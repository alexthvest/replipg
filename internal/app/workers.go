package app

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Worker executes one bounded unit of feature work without owning goroutines.
type Worker interface {
	RunOnce(context.Context) (bool, error)
}

// WorkerFactory constructs a named critical worker before update admission.
type WorkerFactory struct {
	Name string
	New  func(runtimecontract.Transactor, runtimecontract.GroupMenuWriter) (Worker, error)
}

func validateWorkers(factories []WorkerFactory) error {
	seen := map[string]bool{"health_server": true, "outbox_worker": true, "scheduler_worker": true, "intake": true}
	for _, factory := range factories {
		if factory.Name == "" || len(factory.Name) > 64 || factory.New == nil || seen[factory.Name] {
			return errors.New("runtime: invalid worker factory")
		}

		for _, c := range factory.Name {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
				return errors.New("runtime: invalid worker name")
			}
		}

		seen[factory.Name] = true
	}
	return nil
}

func createWorker(factory WorkerFactory, tx runtimecontract.Transactor, menus runtimecontract.GroupMenuWriter) (worker Worker, err error) {
	defer func() {
		if recover() != nil {
			worker = nil
			err = errors.New("runtime: worker factory failed")
		}
	}()

	worker, err = factory.New(tx, menus)
	if err != nil {
		return nil, errors.New("runtime: worker factory failed")
	}

	if worker == nil {
		return nil, errors.New("runtime: invalid worker")
	}

	v := reflect.ValueOf(worker)
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Func || v.Kind() == reflect.Interface || v.Kind() == reflect.Map || v.Kind() == reflect.Slice || v.Kind() == reflect.Chan) && v.IsNil() {
		return nil, errors.New("runtime: invalid worker")
	}

	return worker, nil
}

func (r *Runtime) runFeatureWorker(ctx context.Context, worker Worker, admission context.Context) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("runtime: worker failed")
		}
	}()

	for {
		if err := r.admit(admission); err != nil {
			return err
		}

		done, err := worker.RunOnce(ctx)
		if err != nil {
			return err
		}

		if !done {
			timer := time.NewTimer(time.Second)
			select {
			case <-admission.Done():
				timer.Stop()
				return admission.Err()
			case <-timer.C:
			}
		}
	}
}
