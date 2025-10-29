package contractStore

import (
	"github.com/Layr-Labs/chain-indexer/pkg/config"
	"github.com/Layr-Labs/chain-indexer/pkg/contracts"
)

type IContractStore interface {
	GetContractByAddress(address string) (*contracts.Contract, error)
	GetContractByNameForChainId(name string, chainId config.ChainId) (*contracts.Contract, error)
	ListContractAddressesForChain(chainId config.ChainId) []string
	ListContracts() []*contracts.Contract
	OverrideContract(contractName string, chainIds []config.ChainId, contract *contracts.Contract) error
}
