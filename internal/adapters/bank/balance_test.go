package bank

import (
	"strings"
	"testing"
)

func TestBalanceProtocol(t *testing.T) {
	p := Protocol{}
	body, err := p.BalanceRequest("read", 2)
	if err != nil || body != `{"jsonrpc":"2.0","id":"read","method":"bank.balance.public.v1","params":{"accountId":2}}` {
		t.Fatalf("request: %s %v", body, err)
	}

	for _, amount := range []string{"0", "123", strings.Repeat("9", 100)} {
		reply := `{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":"` + amount + `"}}}`
		got, err := p.DecodeBalance("read", 2, reply)
		if err != nil || got != amount {
			t.Fatalf("balance %s: %s %v", amount, got, err)
		}
	}

	for _, reply := range []string{
		`{"jsonrpc":"2.0","id":"other","result":{"data":{"accountId":2,"balance":"1"}}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":3,"balance":"1"}}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":1}}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":"01"}}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":"-1"}}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":""}}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":"1","balance":"2"}}}`,
		`{"jsonrpc":"2.0","id":"read","error":{"code":-32601,"message":"Method unavailable"}}`,
		`{"jsonrpc":"2.0","id":"read","result":{"data":{"accountId":2,"balance":"` + strings.Repeat("9", 101) + `"}}}`,
	} {
		if _, err := p.DecodeBalance("read", 2, reply); err == nil {
			t.Errorf("accepted invalid reply %s", reply)
		}
	}

	for _, account := range []int64{0, -1, 2147483648} {
		if _, err := p.BalanceRequest("read", account); err == nil {
			t.Errorf("accepted invalid account %d", account)
		}
	}
}
