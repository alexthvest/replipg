package bank

import (
	"encoding/json"
	"errors"
	"fmt"

	app "github.com/alexthvest/replipg/internal/application/shop"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

const Balance = "bank.balance.public.v1"

func (p Protocol) BalanceRequest(id string, accountID int64) (string, error) {
	if accountID <= 0 || accountID > 2147483647 {
		return "", errors.New("bank: invalid balance account")
	}

	params, err := json.Marshal(struct {
		AccountID int64 `json:"accountId"`
	}{AccountID: accountID})
	if err != nil {
		return "", err
	}
	return p.Encode(app.Request{
		ID:     id,
		Method: Balance,
		Params: params,
	})
}

func (Protocol) DecodeBalance(id string, accountID int64, text string) (string, error) {
	replyID, err := ResponseID(text)
	if err != nil || replyID != id {
		return "", errors.New("bank: uncorrelated balance reply")
	}

	var envelope struct {
		Version string `json:"jsonrpc"`
		ID      string `json:"id"`
		Result  *struct {
			Data struct {
				AccountID int64  `json:"accountId"`
				Balance   string `json:"balance"`
			} `json:"data"`
		} `json:"result,omitempty"`
		Error *rpcError `json:"error,omitempty"`
	}
	if err = strict([]byte(text), &envelope); err != nil {
		return "", err
	}
	if envelope.Error != nil {
		return "", fmt.Errorf("bank: balance RPC error %d", envelope.Error.Code)
	}
	if envelope.Result == nil || envelope.Result.Data.AccountID != accountID {
		return "", errors.New("bank: balance account mismatch")
	}

	amount := envelope.Result.Data.Balance
	if amount != "0" {
		if err = domain.Amount(amount).Validate(); err != nil {
			return "", err
		}
	}
	return amount, nil
}
