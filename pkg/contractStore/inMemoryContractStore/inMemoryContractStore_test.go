package inMemoryContractStore

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/contracts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestStore(initial []*contracts.Contract) *InMemoryContractStore {
	return NewInMemoryContractStore(initial, zap.NewNop())
}

func TestAddContract_Success(t *testing.T) {
	store := newTestStore(nil)
	c := &contracts.Contract{Name: "Test", Address: "0xabc", ChainId: config.ChainId(1)}

	err := store.AddContract(c)
	require.NoError(t, err)

	got, err := store.GetContractByAddress("0xabc")
	require.NoError(t, err)
	assert.Equal(t, "Test", got.Name)
}

func TestAddContract_Duplicate_ReturnsError(t *testing.T) {
	store := newTestStore(nil)
	c := &contracts.Contract{Name: "Test", Address: "0xABC", ChainId: config.ChainId(1)}

	err := store.AddContract(c)
	require.NoError(t, err)

	err = store.AddContract(&contracts.Contract{Name: "Test2", Address: "0xabc", ChainId: config.ChainId(1)})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "contract already exists")
}

func TestAddContract_DifferentChain_OK(t *testing.T) {
	store := newTestStore(nil)

	err := store.AddContract(&contracts.Contract{Name: "Test", Address: "0xabc", ChainId: config.ChainId(1)})
	require.NoError(t, err)

	err = store.AddContract(&contracts.Contract{Name: "Test", Address: "0xabc", ChainId: config.ChainId(2)})
	require.NoError(t, err)

	all := store.ListContracts()
	assert.Len(t, all, 2)
}

func TestAddContract_ReflectedInList(t *testing.T) {
	store := newTestStore(nil)

	err := store.AddContract(&contracts.Contract{Name: "Test", Address: "0xabc", ChainId: config.ChainId(1)})
	require.NoError(t, err)

	addrs := store.ListContractAddressesForChain(config.ChainId(1))
	assert.Contains(t, addrs, "0xabc")
}

func TestConcurrentAddAndRead(t *testing.T) {
	store := newTestStore(nil)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			_ = store.AddContract(&contracts.Contract{
				Name:    fmt.Sprintf("c%d", idx),
				Address: fmt.Sprintf("0x%d", idx),
				ChainId: config.ChainId(1),
			})
		}(i)
		go func() {
			defer wg.Done()
			_ = store.ListContracts()
		}()
	}

	wg.Wait()
	all := store.ListContracts()
	assert.Len(t, all, 10)
}

func TestConcurrentOverrideAndGet(t *testing.T) {
	initial := []*contracts.Contract{
		{Name: "Test", Address: "0xoriginal", ChainId: config.ChainId(1)},
	}
	store := newTestStore(initial)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			_ = store.OverrideContract("Test", []config.ChainId{1}, &contracts.Contract{
				Address: fmt.Sprintf("0x%d", idx),
			})
		}(i)
		go func() {
			defer wg.Done()
			_, _ = store.GetContractByAddress("0xoriginal")
		}()
	}

	wg.Wait()
}

func TestListContracts_ReturnsCopy(t *testing.T) {
	store := newTestStore([]*contracts.Contract{
		{Name: "A", Address: "0xa", ChainId: config.ChainId(1)},
	})

	list := store.ListContracts()
	list[0] = nil

	fromStore := store.ListContracts()
	assert.NotNil(t, fromStore[0])
}
