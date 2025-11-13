package integration_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/ethereum-optimism/infra/proxyd"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

const testFlashbotsPrivKeyHex = "59c6995e998f97a5a0044966f0945389dc9e86dae88c6c5d0441ef3e59cb8c27"

func TestBackendDynamicHeaders(t *testing.T) {

	upstreamBackend := NewMockBackend(BatchedResponseHandler(200, goodResponse))
	defer upstreamBackend.Close()
	controlBackend := NewMockBackend(BatchedResponseHandler(200, goodResponse))
	defer controlBackend.Close()

	require.NoError(t, os.Setenv("UPSTREAM_RPC_URL", upstreamBackend.URL()))
	require.NoError(t, os.Setenv("CONTROL_RPC_URL", controlBackend.URL()))

	config := ReadConfig("backend_dynamic_headers")

	_, shutdown, err := proxyd.Start(config)
	require.NoError(t, err)
	defer shutdown()

	privKey, err := crypto.HexToECDSA(testFlashbotsPrivKeyHex)
	require.NoError(t, err)
	addr := crypto.PubkeyToAddress(privKey.PublicKey)

	t.Run("upstream backend blocks header", func(t *testing.T) {
		body, err := json.Marshal(NewRPCReq("1", "eth_chainId", nil))
		require.NoError(t, err)

		hashedBody := crypto.Keccak256Hash(body).Hex()
		msgHash := accounts.TextHash([]byte(hashedBody))
		sig, err := crypto.Sign(msgHash, privKey)
		require.NoError(t, err)
		header := fmt.Sprintf("%s:%s", addr.Hex(), hexutil.Encode(sig))

		req, err := http.NewRequest("POST", "http://127.0.0.1:8545", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(proxyd.FlashbotsAuthHeader, header)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		resBody, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, 200, res.StatusCode)
		RequireEqualJSON(t, []byte(goodResponse), resBody)

		requests := upstreamBackend.Requests()
		require.Len(t, requests, 1)
		require.Empty(t, requests[0].Headers.Values(proxyd.FlashbotsAuthHeader))
	})

	t.Run("control backend forwards header", func(t *testing.T) {
		body, err := json.Marshal(NewRPCReq("2", "net_version", nil))
		require.NoError(t, err)

		hashedBody := crypto.Keccak256Hash(body).Hex()
		msgHash := accounts.TextHash([]byte(hashedBody))
		sig, err := crypto.Sign(msgHash, privKey)
		require.NoError(t, err)
		header := fmt.Sprintf("%s:%s", addr.Hex(), hexutil.Encode(sig))

		req, err := http.NewRequest("POST", "http://127.0.0.1:8545", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(proxyd.FlashbotsAuthHeader, header)

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		resBody, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, 200, res.StatusCode)
		RequireEqualJSON(t, []byte(goodResponse), resBody)

		requests := controlBackend.Requests()
		require.Len(t, requests, 1)
		expectedBodyHash := crypto.Keccak256Hash(requests[0].Body).Hex()
		expectedMsgHash := accounts.TextHash([]byte(expectedBodyHash))
		expectedSig, err := crypto.Sign(expectedMsgHash, privKey)
		require.NoError(t, err)
		expectedHeader := fmt.Sprintf("%s:%s", addr.Hex(), hexutil.Encode(expectedSig))

		require.Equal(t, []string{expectedHeader}, requests[0].Headers.Values(proxyd.FlashbotsAuthHeader))
	})
}
