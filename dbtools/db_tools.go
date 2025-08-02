package dbtools

import "github.com/google/blueprint/syncmap"

type KeyValueStore interface {
	Put(key []byte, value []byte) error
	Get(key []byte) ([]byte, error)
	Close() error
}

type InMemKeyValueStore struct {
	data syncmap.SyncMap[string, []byte]
}

func (s *InMemKeyValueStore) Close() error {
	return nil
}

func (s *InMemKeyValueStore) Put(key []byte, value []byte) error {
	s.data.LoadOrStore(string(key), value)
	return nil
}

func (s *InMemKeyValueStore) Get(key []byte) ([]byte, error) {
	if ret, ok := s.data.Load(string(key)); !ok {
		return nil, nil
	} else {
		return ret, nil
	}
}
