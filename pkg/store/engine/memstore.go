package engine

import (
	"sync"

	"github.com/rancher/apiserver-tests/pkg/server/handlers"
	"github.com/rancher/apiserver/pkg/apierror"
	"github.com/rancher/apiserver/pkg/types"
	"github.com/rancher/wrangler/v3/pkg/schemas/validation"
)

type MemoryStore struct {
	mu        sync.RWMutex
	items     map[string]types.APIObject
	watchers  map[int]chan types.APIEvent
	watcherID int
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items:    make(map[string]types.APIObject),
		watchers: make(map[int]chan types.APIEvent),
	}
}

func (e *MemoryStore) ByID(apiOp *types.APIRequest, schema *types.APISchema, id string) (types.APIObject, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	val, ok := e.items[id]
	if !ok {
		return types.APIObject{}, apierror.NewAPIError(validation.NotFound, "Resource not found")
	}
	return val, nil
}

func (e *MemoryStore) List(apiOp *types.APIRequest, schema *types.APISchema) (types.APIObjectList, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var list types.APIObjectList
	for _, v := range e.items {
		list.Objects = append(list.Objects, v)
	}
	return list, nil
}

func (e *MemoryStore) Create(apiOp *types.APIRequest, schema *types.APISchema, data types.APIObject) (types.APIObject, error) {
	if err := handlers.WidgetValidator(apiOp, schema, data); err != nil {
		return types.APIObject{}, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	dataMap := data.Data()
	name, _ := dataMap["name"].(string)
	if name == "" {
		return types.APIObject{}, apierror.NewAPIError(validation.MissingRequired, "Resource name is mandatory")
	}

	obj := types.APIObject{
		ID:     name,
		Type:   "widget",
		Object: dataMap,
	}
	e.items[name] = obj

	e.broadcast(types.APIEvent{Name: "change", Object: obj})
	return obj, nil
}

func (e *MemoryStore) Update(apiOp *types.APIRequest, schema *types.APISchema, data types.APIObject, id string) (types.APIObject, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.items[id]; !ok {
		return types.APIObject{}, apierror.NewAPIError(validation.NotFound, "Resource not found")
	}

	obj := types.APIObject{
		ID:     id,
		Type:   "widget",
		Object: data.Data(),
	}
	e.items[id] = obj
	e.broadcast(types.APIEvent{Name: "change", Object: obj})
	return obj, nil
}

func (e *MemoryStore) Delete(apiOp *types.APIRequest, schema *types.APISchema, id string) (types.APIObject, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	obj, ok := e.items[id]
	if !ok {
		return types.APIObject{}, apierror.NewAPIError(validation.NotFound, "Resource not found")
	}

	delete(e.items, id)
	e.broadcast(types.APIEvent{Name: "remove", Object: obj})
	return obj, nil
}

// Helper to handle broadcasting to all active watchers
func (e *MemoryStore) broadcast(event types.APIEvent) {
	for _, ch := range e.watchers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (e *MemoryStore) Watch(apiOp *types.APIRequest, schema *types.APISchema, w types.WatchRequest) (chan types.APIEvent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := make(chan types.APIEvent, 256)
	e.watcherID++
	id := e.watcherID
	e.watchers[id] = ch

	ctx := apiOp.Context()
	go func() {
		<-ctx.Done()
		e.mu.Lock()
		delete(e.watchers, id)
		close(ch)
		e.mu.Unlock()
	}()

	return ch, nil
}