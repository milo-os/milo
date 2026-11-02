package cockroach

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

func (s *store) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	preparedKey, err := s.prepareKey(key, false)
	if err != nil {
		return err
	}
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		return storage.ErrResourceVersionSetOnCreate
	}
	if err := s.versioner.PrepareObjectForStorage(obj); err != nil {
		return fmt.Errorf("PrepareObjectForStorage failed: %v", err)
	}
	data, err := runtime.Encode(s.codec, obj)
	if err != nil {
		return err
	}
	newData, err := s.transformer.TransformToStorage(ctx, data, s.authData(preparedKey))
	if err != nil {
		return storage.NewInternalError(err)
	}
	res, err := s.b.committer.commit(ctx, s.project, createWrite(preparedKey, newData, ttl))
	switch {
	case err != nil:
		return err
	case !res.written:
		return storage.NewKeyExistsError(preparedKey, 0)
	case out != nil:
		return s.decoder.Decode(data, out, res.rev)
	}
	return nil
}

// objState is an object as read, decoded and versioned.
type objState struct {
	obj   runtime.Object
	meta  *storage.ResponseMeta
	rev   int64
	data  []byte
	stale bool
}

// getState reads live into a state.
func (s *store) getState(ctx context.Context, live *row, key string, v reflect.Value, ignoreNotFound bool) (*objState, error) {
	state := &objState{meta: &storage.ResponseMeta{}}
	if u, ok := v.Addr().Interface().(runtime.Unstructured); ok {
		state.obj = u.NewEmptyInstance()
	} else {
		state.obj = reflect.New(v.Type()).Interface().(runtime.Object)
	}
	if live == nil {
		if !ignoreNotFound {
			return nil, storage.NewKeyNotFoundError(key, 0)
		}
		if err := runtime.SetZeroValue(state.obj); err != nil {
			return nil, err
		}
		return state, nil
	}
	state.rev = live.rev
	state.meta.ResourceVersion = uint64(live.rev)
	data, stale, err := s.transformer.TransformFromStorage(ctx, live.value, s.authData(key))
	if err != nil {
		return nil, storage.NewInternalError(err)
	}
	state.data, state.stale = data, stale
	if err := s.decoder.Decode(data, state.obj, state.rev); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *store) getCurrentState(ctx context.Context, key string, v reflect.Value, ignoreNotFound bool) func() (*objState, error) {
	return func() (*objState, error) {
		live, _, err := s.readKey(ctx, key)
		if err != nil {
			return nil, err
		}
		return s.getState(ctx, live, key, v, ignoreNotFound)
	}
}

func (s *store) getStateFromObject(obj runtime.Object) (*objState, error) {
	state := &objState{obj: obj, meta: &storage.ResponseMeta{}}
	rv, err := s.versioner.ObjectResourceVersion(obj)
	if err != nil {
		return nil, fmt.Errorf("couldn't get resource version: %v", err)
	}
	state.rev = int64(rv)
	state.meta.ResourceVersion = rv
	// The serialized form is stored without a resource version.
	if err := s.versioner.PrepareObjectForStorage(obj); err != nil {
		return nil, fmt.Errorf("PrepareObjectForStorage failed: %v", err)
	}
	state.data, err = runtime.Encode(s.codec, obj)
	if err != nil {
		return nil, err
	}
	if err := s.versioner.UpdateObject(state.obj, rv); err != nil {
		return nil, fmt.Errorf("couldn't restore resource version: %v", err)
	}
	return state, nil
}

// Delete and GuaranteedUpdate follow etcd3's, with a conditional write in
// place of its transaction.

func (s *store) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions,
	validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	preparedKey, err := s.prepareKey(key, false)
	if err != nil {
		return err
	}
	v, err := conversion.EnforcePtr(out)
	if err != nil {
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}
	getCurrentState := s.getCurrentState(ctx, preparedKey, v, false)

	var origState *objState
	var origStateIsCurrent bool
	if cachedExistingObject != nil {
		origState, err = s.getStateFromObject(cachedExistingObject)
	} else {
		origState, err = getCurrentState()
		origStateIsCurrent = true
	}
	if err != nil {
		return err
	}

	for {
		err := preconditions.Check(preparedKey, origState.obj)
		if err == nil {
			err = validateDeletion(ctx, origState.obj)
		}
		if err != nil {
			if origStateIsCurrent {
				return err
			}
			cachedRev, cachedErr := origState.rev, err
			if origState, err = getCurrentState(); err != nil {
				return err
			}
			origStateIsCurrent = true
			if cachedRev == origState.rev {
				return cachedErr
			}
			continue
		}

		res, err := s.b.committer.commit(ctx, s.project, deleteWrite(preparedKey, origState.rev))
		if err != nil {
			return err
		}
		if !res.written {
			if origState, err = s.getState(ctx, res.live, preparedKey, v, false); err != nil {
				return err
			}
			origStateIsCurrent = true
			continue
		}
		return s.decoder.Decode(origState.data, out, res.rev)
	}
}

func (s *store) GuaranteedUpdate(ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	preparedKey, err := s.prepareKey(key, false)
	if err != nil {
		return err
	}
	v, err := conversion.EnforcePtr(destination)
	if err != nil {
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}
	getCurrentState := s.getCurrentState(ctx, preparedKey, v, ignoreNotFound)

	var origState *objState
	var origStateIsCurrent bool
	if cachedExistingObject != nil {
		origState, err = s.getStateFromObject(cachedExistingObject)
	} else {
		origState, err = getCurrentState()
		origStateIsCurrent = true
	}
	if err != nil {
		return err
	}

	for {
		if err := preconditions.Check(preparedKey, origState.obj); err != nil {
			if origStateIsCurrent {
				return err
			}
			if origState, err = getCurrentState(); err != nil {
				return err
			}
			origStateIsCurrent = true
			continue
		}

		ret, ttl, err := s.updateState(origState, tryUpdate)
		if err != nil {
			if origStateIsCurrent {
				return err
			}
			cachedRev, cachedErr := origState.rev, err
			if origState, err = getCurrentState(); err != nil {
				return err
			}
			origStateIsCurrent = true
			if cachedRev == origState.rev {
				return cachedErr
			}
			continue
		}
		data, err := runtime.Encode(s.codec, ret)
		if err != nil {
			return err
		}
		if !origState.stale && bytes.Equal(data, origState.data) {
			// A no-op update writes nothing, once the stored data is known to match.
			if !origStateIsCurrent {
				if origState, err = getCurrentState(); err != nil {
					return err
				}
				origStateIsCurrent = true
				if !bytes.Equal(data, origState.data) {
					continue
				}
			}
			if !origState.stale {
				return s.decoder.Decode(origState.data, destination, origState.rev)
			}
		}

		newData, err := s.transformer.TransformToStorage(ctx, data, s.authData(preparedKey))
		if err != nil {
			return storage.NewInternalError(err)
		}
		res, err := s.b.committer.commit(ctx, s.project, updateWrite(preparedKey, origState.rev, newData, ttl))
		if err != nil {
			return err
		}
		if !res.written {
			if origState, err = s.getState(ctx, res.live, preparedKey, v, ignoreNotFound); err != nil {
				return err
			}
			origStateIsCurrent = true
			continue
		}
		return s.decoder.Decode(data, destination, res.rev)
	}
}

func (s *store) updateState(st *objState, userUpdate storage.UpdateFunc) (runtime.Object, uint64, error) {
	ret, ttlPtr, err := userUpdate(st.obj, *st.meta)
	if err != nil {
		return nil, 0, err
	}
	if err := s.versioner.PrepareObjectForStorage(ret); err != nil {
		return nil, 0, fmt.Errorf("PrepareObjectForStorage failed: %v", err)
	}
	var ttl uint64
	if ttlPtr != nil {
		ttl = *ttlPtr
	}
	return ret, ttl, nil
}
