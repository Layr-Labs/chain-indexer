package ethereum

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetLogsForAddressesRequest_MultipleAddresses(t *testing.T) {
	req := GetLogsForAddressesRequest([]string{"0xA", "0xB", "0xC"}, 100, 200, 1)

	assert.Equal(t, "eth_getLogs", req.Method)
	assert.Equal(t, "2.0", req.JSONRPC)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &parsed))

	params := parsed["params"].([]interface{})
	filter := params[0].(map[string]interface{})

	addrs := filter["address"].([]interface{})
	assert.Len(t, addrs, 3)
	assert.Equal(t, "0xA", addrs[0])
	assert.Equal(t, "0xB", addrs[1])
	assert.Equal(t, "0xC", addrs[2])

	assert.Equal(t, "0x64", filter["fromBlock"])
	assert.Equal(t, "0xc8", filter["toBlock"])
}

func TestGetLogsForAddressesRequest_SingleAddress(t *testing.T) {
	req := GetLogsForAddressesRequest([]string{"0xABC"}, 0, 0, 1)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &parsed))

	params := parsed["params"].([]interface{})
	filter := params[0].(map[string]interface{})

	addrs := filter["address"].([]interface{})
	assert.Len(t, addrs, 1)
	assert.Equal(t, "0xABC", addrs[0])
}

func TestGetLogsRequest_SingleAddressString(t *testing.T) {
	req := GetLogsRequest("0xABC", 10, 20, 1)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &parsed))

	params := parsed["params"].([]interface{})
	filter := params[0].(map[string]interface{})

	addr, ok := filter["address"].(string)
	assert.True(t, ok)
	assert.Equal(t, "0xABC", addr)
}

func TestGetLogsForAddressesRequest_BlockRangeEncoding(t *testing.T) {
	req := GetLogsForAddressesRequest([]string{"0x1"}, 255, 4096, 1)

	body, err := json.Marshal(req)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(body, &parsed))

	params := parsed["params"].([]interface{})
	filter := params[0].(map[string]interface{})

	assert.Equal(t, "0xff", filter["fromBlock"])
	assert.Equal(t, "0x1000", filter["toBlock"])
}
