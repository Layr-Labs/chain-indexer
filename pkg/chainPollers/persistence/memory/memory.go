package memory

import (
	"context"
	"fmt"
	"sync"

	chainPoller "github.com/Layr-Labs/chain-indexer/pkg/chainPollers"
	"github.com/Layr-Labs/chain-indexer/pkg/chainPollers/persistence"
	"github.com/Layr-Labs/chain-indexer/pkg/config"
)

// InMemoryChainPollerPersistence implements AggregatorStore interface with in-memory storage
type InMemoryChainPollerPersistence struct {
	mu                  sync.RWMutex
	closed              bool
	lastProcessedBlocks map[string]uint64
	blocks              map[string]*chainPoller.BlockRecord
}

// NewInMemoryChainPollerPersistence creates a new in-memory aggregator store
func NewInMemoryChainPollerPersistence() *InMemoryChainPollerPersistence {
	return &InMemoryChainPollerPersistence{
		lastProcessedBlocks: make(map[string]uint64),
		blocks:              make(map[string]*chainPoller.BlockRecord),
	}
}

// makeBlockKey creates a composite key for last processed block storage
func makeBlockKey(chainId config.ChainId) string {
	return fmt.Sprintf("%d", chainId)
}

// makeBlockRecordKey creates a composite key for block info storage
func makeBlockRecordKey(chainId config.ChainId, blockNumber uint64) string {
	return fmt.Sprintf("block:%d:%d", chainId, blockNumber)
}

// GetLastProcessedBlock returns the last processed block for a chain for a specific AVS
func (s *InMemoryChainPollerPersistence) GetLastProcessedBlock(ctx context.Context, chainId config.ChainId) (*chainPoller.BlockRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, persistence.ErrStoreClosed
	}

	key := makeBlockKey(chainId)
	blockNum, exists := s.lastProcessedBlocks[key]
	if !exists {
		return nil, persistence.ErrNotFound
	}

	blockKey := makeBlockRecordKey(chainId, blockNum)
	block, exists := s.blocks[blockKey]
	if !exists {
		return nil, persistence.ErrNotFound
	}

	return block, nil
}

// SaveBlock saves block information for reorg detection
func (s *InMemoryChainPollerPersistence) SaveBlock(ctx context.Context, block *chainPoller.BlockRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return persistence.ErrStoreClosed
	}

	if block == nil {
		return fmt.Errorf("block cannot be nil")
	}

	key := makeBlockRecordKey(block.ChainId, block.Number)
	s.blocks[key] = block
	key = makeBlockKey(block.ChainId)
	s.lastProcessedBlocks[key] = block.Number

	return nil
}

// GetBlock retrieves block information by block number
func (s *InMemoryChainPollerPersistence) GetBlock(ctx context.Context, chainId config.ChainId, blockNumber uint64) (*chainPoller.BlockRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return nil, persistence.ErrStoreClosed
	}

	key := makeBlockRecordKey(chainId, blockNumber)
	block, exists := s.blocks[key]
	if !exists {
		return nil, persistence.ErrNotFound
	}

	return block, nil
}

// DeleteBlock removes block information from storage
func (s *InMemoryChainPollerPersistence) DeleteBlock(ctx context.Context, chainId config.ChainId, blockNumber uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return persistence.ErrStoreClosed
	}

	key := makeBlockRecordKey(chainId, blockNumber)
	if _, exists := s.blocks[key]; !exists {
		return persistence.ErrNotFound
	}

	delete(s.blocks, key)
	return nil
}

// Close closes the store
func (s *InMemoryChainPollerPersistence) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return persistence.ErrStoreClosed
	}

	s.closed = true

	// Clear all maps
	s.lastProcessedBlocks = nil
	s.blocks = nil

	return nil
}
