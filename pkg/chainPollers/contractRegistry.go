package chainPoller

type ContractRegistry interface {
	RegisterContracts(addresses []string)
	UnregisterContracts(addresses []string)
	ListContracts() []string
}
