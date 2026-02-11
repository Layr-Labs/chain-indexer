package inMemoryContractStore

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/contracts"
	"github.com/Layr-Labs/chain-indexer/pkg/util"
	"go.uber.org/zap"
)

type InMemoryContractStore struct {
	mu        sync.RWMutex
	contracts []*contracts.Contract
	logger    *zap.Logger
}

func NewInMemoryContractStore(contracts []*contracts.Contract, logger *zap.Logger) *InMemoryContractStore {
	return &InMemoryContractStore{
		contracts: contracts,
		logger:    logger,
	}
}

func (ics *InMemoryContractStore) GetContractByNameForChainId(name string, chainId config.ChainId) (*contracts.Contract, error) {
	ics.mu.RLock()
	defer ics.mu.RUnlock()

	foundContract := util.Find(ics.contracts, func(c *contracts.Contract) bool {
		return strings.EqualFold(c.Name, name) && c.ChainId == chainId
	})
	if foundContract == nil {
		ics.logger.Error("Contract not found", zap.String("name", name), zap.Any("chainId", chainId))
		return nil, fmt.Errorf("contract not found: name=%s, chainId=%d", name, chainId)
	}
	return foundContract, nil
}

func (ics *InMemoryContractStore) GetContractByAddress(address string) (*contracts.Contract, error) {
	ics.mu.RLock()
	defer ics.mu.RUnlock()

	address = strings.ToLower(address)
	contract := util.Find(ics.contracts, func(c *contracts.Contract) bool {
		return strings.EqualFold(c.Address, address)
	})
	if contract == nil {
		ics.logger.Error("Contract not found", zap.String("address", address))
		return nil, nil
	}
	return contract, nil
}

func (ics *InMemoryContractStore) ListContractAddressesForChain(chainId config.ChainId) []string {
	ics.mu.RLock()
	defer ics.mu.RUnlock()

	chainContracts := util.Reduce(ics.contracts, func(acc map[string]*contracts.Contract, c *contracts.Contract) map[string]*contracts.Contract {
		if c.ChainId == chainId {
			acc[strings.ToLower(c.Address)] = c
		}
		return acc
	}, make(map[string]*contracts.Contract))
	addresses := make([]string, 0, len(chainContracts))
	for a := range chainContracts {
		addresses = append(addresses, a)
	}
	return addresses
}

func (ics *InMemoryContractStore) OverrideContract(contractName string, chainIds []config.ChainId, contract *contracts.Contract) error {
	ics.mu.Lock()
	defer ics.mu.Unlock()

	found := false
	for i, origContract := range ics.contracts {
		if origContract.Name == contractName && (len(chainIds) == 0 || slices.Contains(chainIds, origContract.ChainId)) {
			ics.logger.Sugar().Infow("Overriding contract",
				zap.String("name", contractName),
				zap.String("previousAddress", origContract.Address),
				zap.String("newAddress", contract.Address),
				zap.Any("chainId", origContract.ChainId),
			)
			ics.contracts[i] = &contracts.Contract{
				Name:        origContract.Name,
				Address:     contract.Address,
				ChainId:     origContract.ChainId,
				AbiVersions: contract.AbiVersions,
			}
			found = true
		}
	}
	if !found {
		ics.logger.Sugar().Infow("Contract not found for override, adding new contract",
			zap.String("name", contractName),
			zap.Any("chainIds", chainIds),
		)
		for _, chainId := range chainIds {
			ics.contracts = append(ics.contracts, &contracts.Contract{
				Name:        contract.Name,
				Address:     contract.Address,
				ChainId:     chainId,
				AbiVersions: contract.AbiVersions,
			})
		}
	}
	return nil
}

func (ics *InMemoryContractStore) AddContract(contract *contracts.Contract) error {
	ics.mu.Lock()
	defer ics.mu.Unlock()

	addr := strings.ToLower(contract.Address)
	for _, c := range ics.contracts {
		if strings.EqualFold(c.Address, addr) && c.ChainId == contract.ChainId {
			return fmt.Errorf("contract already exists: address=%s, chainId=%d", addr, contract.ChainId)
		}
	}
	ics.contracts = append(ics.contracts, contract)
	return nil
}

func (ics *InMemoryContractStore) ListContracts() []*contracts.Contract {
	ics.mu.RLock()
	defer ics.mu.RUnlock()

	out := make([]*contracts.Contract, len(ics.contracts))
	copy(out, ics.contracts)
	return out
}
