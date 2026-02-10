package contractRegistry

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInMemoryContractRegistry_RegisterAndList(t *testing.T) {
	r := NewInMemoryContractRegistry()
	r.RegisterContracts([]string{"0xAAA", "0xBBB"})

	got := r.ListContracts()
	sort.Strings(got)
	assert.Equal(t, []string{"0xaaa", "0xbbb"}, got)
}

func TestInMemoryContractRegistry_Deduplication(t *testing.T) {
	r := NewInMemoryContractRegistry()
	r.RegisterContracts([]string{"0xAAA", "0xaaa", "0xAAA"})

	got := r.ListContracts()
	assert.Len(t, got, 1)
	assert.Equal(t, "0xaaa", got[0])
}

func TestInMemoryContractRegistry_Unregister(t *testing.T) {
	r := NewInMemoryContractRegistry()
	r.RegisterContracts([]string{"0xAAA", "0xBBB", "0xCCC"})
	r.UnregisterContracts([]string{"0xbbb"})

	got := r.ListContracts()
	sort.Strings(got)
	assert.Equal(t, []string{"0xaaa", "0xccc"}, got)
}

func TestInMemoryContractRegistry_UnregisterNonExistent(t *testing.T) {
	r := NewInMemoryContractRegistry()
	r.RegisterContracts([]string{"0xAAA"})
	r.UnregisterContracts([]string{"0xZZZ"})

	got := r.ListContracts()
	assert.Len(t, got, 1)
}

func TestInMemoryContractRegistry_ListReturnsCopy(t *testing.T) {
	r := NewInMemoryContractRegistry()
	r.RegisterContracts([]string{"0xAAA"})

	list := r.ListContracts()
	list[0] = "mutated"

	assert.Equal(t, []string{"0xaaa"}, r.ListContracts())
}

func TestInMemoryContractRegistry_ConcurrentAccess(t *testing.T) {
	r := NewInMemoryContractRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			r.RegisterContracts([]string{fmt.Sprintf("0x%d", idx)})
		}(i)
		go func() {
			defer wg.Done()
			_ = r.ListContracts()
		}()
	}
	wg.Wait()

	got := r.ListContracts()
	assert.Len(t, got, 50)
}
