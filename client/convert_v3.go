package client

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/shopspring/decimal"
	abcitypes "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/crypto"
	tmtypes "github.com/tendermint/tendermint/types"
	"github.com/wolot/api/database"
	"github.com/wolot/api/libs/log"
	"github.com/wolot/api/mondo/types"
	"go.uber.org/zap"
	"math/big"
	"strconv"
)

type V3BlockData struct {
	ledger   *database.V3Ledger
	txs      []database.V3Transaction
	payments []database.V3Payment
}

func (cli *Client) GetV3BlockData(height int64) (*V3BlockData, error) {
	data := V3BlockData{}

	blockResult, err := cli.fetch.FetchBlockInfo(height)
	if err != nil {
		return nil, err
	}

	data.ledger = &database.V3Ledger{
		Height:     height,
		BlockHash:  blockResult.Block.Hash().String(),
		BlockSize:  blockResult.Block.Size(),
		Validator:  blockResult.Block.ProposerAddress.String(),
		TxCount:    int64(len(blockResult.Block.Data.Txs)),
		GasLimit:   0,
		GasUsed:    0,
		GasPrice:   "1",
		CreatedAt:  blockResult.Block.Time,
		TotalPrice: new(big.Int),
	}

	deliverResult, err := cli.fetch.FetchBlockResultInfo(height)
	if err != nil {
		return nil, err
	}

	for txIdx, bs := range blockResult.Block.Txs {
		if len(bs) <= 2 {
			log.Logger.Warn("error tx", zap.ByteString("tx", bs))
			continue
		}
		switch {
		case bytes.HasPrefix(bs, types.TxTagAppEvm):
			{
				var tx types.TxEvm
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxAppEvm(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		case bytes.HasPrefix(bs, types.TxTagAppBatch):
			{
				var tx types.TxBatch
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxAppBatch(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		case bytes.HasPrefix(bs, types.TxTagNodeDelegate):
			{
				var tx types.TxNodeDelegate
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxNodeDelegate(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		case bytes.HasPrefix(bs, types.TxTagUserDelegate):
			{
				var tx types.TxUserDelegate
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxUserDelegate(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		case bytes.HasPrefix(bs, types.TxTagAppEvmMultisig):
			{
				var tx types.MultisigEvmTx
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxMultisigEvm(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		case bytes.HasPrefix(bs, types.TxTagAppParams):
			{
				var tx types.TxParams
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxParams(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		case bytes.HasPrefix(bs, types.TxTagAppMgr):
			{
				var tx types.TxManage
				if err := tx.FromBytes(bs[2:]); err != nil {
					log.Logger.Warn("FromBytes", zap.Error(err), zap.ByteString("tx", bs[2:]))
					continue
				}
				transaction, payments := cli.DecodeTxManage(&tx, blockResult.Block, deliverResult[txIdx], data.ledger)
				data.txs = append(data.txs, *transaction)
				data.payments = append(data.payments, payments...)
			}
		default:
			log.Logger.Warn("unknown txTag", zap.Int("txTag", txTagToTypei(bs[:2])))
			continue
		}
	}

	// 计算平均gasPrice
	if data.ledger.TxCount > 0 {
		data.ledger.GasPrice = decimal.NewFromBigInt(data.ledger.TotalPrice, 0).Div(decimal.New(data.ledger.TxCount, 0)).Round(2).String()
		//data.ledger.GasPrice = new(big.Int).Div(data.ledger.TotalPrice, big.NewInt(data.ledger.TxCount)).String()
	}

	return &data, nil
}

func (cli *Client) DecodeTxAppEvm(tx *types.TxEvm, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {
	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagAppEvm),
		Types:     "TxTagAppEvm",
		Sender:    tx.Sender.ToAddress().Hex(),
		Nonce:     int64(tx.Nonce),
		Receiver:  tx.Body.To.ToAddress().Hex(),
		Value:     tx.Body.Value.String(),
		GasLimit:  int64(tx.GasLimit),
		GasUsed:   deliverResult.GasUsed,
		GasPrice:  tx.GasPrice.String(),
		Memo:      string(tx.Body.Memo),
		Payload:   hex.EncodeToString(tx.Body.Load),
		Events:    deliverResult.GetInfo(),
		Codei:     deliverResult.Code,
		Codes:     deliverResult.Log,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(tx.GasLimit)
	ledger.GasUsed += deliverResult.GasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, tx.GasPrice)

	if deliverResult.Code != 0 {
		return trans, nil
	}

	var (
		payments []database.V3Payment
	)

	if tx.Body.Value.Cmp(new(big.Int)) > 0 {
		payments = append(payments, database.V3Payment{
			Hash:      tx.Hash().Hex(),
			Height:    block.Height,
			Idx:       0,
			Sender:    tx.Sender.ToAddress().Hex(),
			Receiver:  tx.Body.To.ToAddress().Hex(),
			Symbol:    "OLO",
			Contract:  common.Address{}.Hex(),
			Value:     tx.Body.Value.String(),
			CreatedAt: block.Time,
		})
	}
	subPays, err := cli.resolveTxEvents(trans)
	if err != nil {
		return trans, payments
	}
	payments = append(payments, subPays...)
	return trans, payments
}

func (cli *Client) DecodeTxMultisigEvm(tx *types.MultisigEvmTx, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {
	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagAppEvmMultisig),
		Types:     "TxTagAppEvmMultisig",
		Sender:    tx.From.Hex(),
		Nonce:     int64(tx.Nonce),
		Receiver:  tx.To.Hex(),
		Value:     tx.Value.String(),
		GasLimit:  int64(tx.GasLimit),
		GasUsed:   deliverResult.GasUsed,
		GasPrice:  tx.GasPrice.String(),
		Memo:      string(tx.Memo),
		Payload:   hex.EncodeToString(tx.Load),
		Events:    deliverResult.GetInfo(),
		Codei:     deliverResult.Code,
		Codes:     deliverResult.Log,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(tx.GasLimit)
	ledger.GasUsed += deliverResult.GasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, tx.GasPrice)

	if deliverResult.Code != 0 {
		return trans, nil
	}

	var (
		payments []database.V3Payment
	)

	if tx.Value.Cmp(new(big.Int)) > 0 {
		payments = append(payments, database.V3Payment{
			Hash:      tx.Hash().Hex(),
			Height:    block.Height,
			Idx:       0,
			Sender:    tx.From.Hex(),
			Receiver:  tx.To.Hex(),
			Symbol:    "OLO",
			Contract:  common.Address{}.Hex(),
			Value:     tx.Value.String(),
			CreatedAt: block.Time,
		})
	}
	subPays, err := cli.resolveTxEvents(trans)
	if err != nil {
		return trans, payments
	}
	payments = append(payments, subPays...)
	return trans, payments
}

func (cli *Client) DecodeTxAppBatch(tx *types.TxBatch, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {
	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagAppBatch),
		Types:     "TxTagAppBatch",
		Sender:    tx.Sender.ToAddress().Hex(),
		Nonce:     int64(tx.Nonce),
		Receiver:  "",
		Value:     "",
		GasLimit:  int64(tx.GasLimit),
		GasUsed:   deliverResult.GasUsed,
		GasPrice:  tx.GasPrice.String(),
		Memo:      string(tx.Memo),
		Payload:   "",
		Events:    "",
		Codei:     deliverResult.Code,
		Codes:     deliverResult.Log,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(tx.GasLimit)
	ledger.GasUsed += deliverResult.GasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, tx.GasPrice)

	if deliverResult.Code != 0 {
		return trans, nil
	}

	var payments []database.V3Payment
	for idx, v := range tx.Ops {
		payments = append(payments, database.V3Payment{
			Hash:      tx.Hash().Hex(),
			Height:    block.Height,
			Idx:       uint(idx),
			Sender:    tx.Sender.ToAddress().Hex(),
			Receiver:  v.To.ToAddress().Hex(),
			Symbol:    "OLO",
			Contract:  common.Address{}.Hex(),
			Value:     v.Value.String(),
			CreatedAt: block.Time,
		})
	}

	return trans, payments
}

func (cli *Client) DecodeTxNodeDelegate(tx *types.TxNodeDelegate, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {

	var (
		receiver string
		memo     string
	)
	switch tx.OpType {
	case types.NODE_OPTYPE_MORTGAGE:
		memo = fmt.Sprintf("node delegate opType:%d(%s) opValue:%s", tx.OpType, "MORTGAGE", tx.OpValue.String())
	case types.NODE_OPTYPE_REEDEM:
		memo = fmt.Sprintf("node delegate opType:%d(%s) opValue:%s", tx.OpType, "REEDEM", tx.OpValue.String())
	case types.NODE_OPTYPE_COLLECT:
		memo = fmt.Sprintf("node delegate opType:%d(%s) opValue:%s", tx.OpType, "COLLECT", tx.OpValue.String())
	case types.NODE_OPTYPE_WITHDRAW:
		memo = fmt.Sprintf("node delegate opType:%d(%s) opValue:%s", tx.OpType, "WITHDRAW", tx.OpValue.String())
		receiver = common.BytesToAddress(tx.Receiver).Hex()
	}

	// todo 为什么deliverResult会是nil?
	var (
		gasUsed int64
		codei   uint32
		codes   string
	)
	if deliverResult == nil {
		gasUsed = 0
		codei = 0
		codes = ""
		log.Logger.Warn("DecodeTxNodeDelegate deliverResult is null", zap.Int64("height", block.Height))
	} else {
		gasUsed = deliverResult.GasUsed
		codei = deliverResult.Code
		codes = deliverResult.Log
	}

	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagNodeDelegate),
		Types:     "TxTagNodeDelegate",
		Sender:    tx.Sender.Address().String(),
		Nonce:     int64(tx.Nonce),
		Receiver:  receiver,
		Value:     tx.OpValue.String(),
		GasLimit:  21000,
		GasUsed:   gasUsed,
		GasPrice:  "1",
		Memo:      memo,
		Payload:   "",
		Events:    "",
		Codei:     codei,
		Codes:     codes,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(0)
	ledger.GasUsed += gasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, new(big.Int).SetUint64(1))

	return trans, nil
}

func (cli *Client) DecodeTxUserDelegate(tx *types.TxUserDelegate, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {

	var (
		receiver string
		memo     string
	)
	switch tx.OpType {
	case types.USER_OPTYPE_MORTGAGE:
		memo = fmt.Sprintf("user delegate opType:%d(%s) opValue:%s", tx.OpType, "MORTGAGE", tx.OpValue.String())
		receiver = crypto.Address(tx.Receiver).String()
	case types.USER_OPTYPE_REEDEM:
		memo = fmt.Sprintf("user delegate opType:%d(%s) opValue:%s", tx.OpType, "REEDEM", tx.OpValue.String())
	case types.USER_OPTYPE_COLLECT:
		memo = fmt.Sprintf("user delegate opType:%d(%s) opValue:%s", tx.OpType, "COLLECT", tx.OpValue.String())
	}

	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagUserDelegate),
		Types:     "TxTagUserDelegate",
		Sender:    tx.Sender.ToAddress().Hex(),
		Nonce:     int64(tx.Nonce),
		Receiver:  receiver,
		Value:     tx.OpValue.String(),
		GasLimit:  21000,
		GasUsed:   deliverResult.GasUsed,
		GasPrice:  "1",
		Memo:      memo,
		Payload:   "",
		Events:    "",
		Codei:     deliverResult.Code,
		Codes:     deliverResult.Log,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(0)
	ledger.GasUsed += deliverResult.GasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, new(big.Int).SetUint64(1))

	return trans, nil
}

func (cli *Client) DecodeTxManage(tx *types.TxManage, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {

	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagAppMgr),
		Types:     "TxTagAppMgr",
		Sender:    tx.Sender.Address().String(),
		Nonce:     int64(tx.Nonce),
		Receiver:  tx.Receiver.Address().String(),
		Value:     strconv.FormatUint(tx.OpValue, 10),
		GasLimit:  0,
		GasUsed:   deliverResult.GasUsed,
		GasPrice:  "1",
		Memo:      fmt.Sprintf("manage opType:%d opValue:%d", tx.OpType, tx.OpValue),
		Payload:   "",
		Events:    "",
		Codei:     deliverResult.Code,
		Codes:     deliverResult.Log,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(0)
	ledger.GasUsed += deliverResult.GasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, new(big.Int).SetUint64(1))

	return trans, nil
}

func (cli *Client) DecodeTxParams(tx *types.TxParams, block *tmtypes.Block, deliverResult *abcitypes.ResponseDeliverTx,
	ledger *database.V3Ledger) (*database.V3Transaction, []database.V3Payment) {

	trans := &database.V3Transaction{
		Hash:      tx.Hash().Hex(),
		Height:    block.Height,
		Typei:     txTagToTypei(types.TxTagAppParams),
		Types:     "TxTagAppParams",
		Sender:    tx.Sender.Address().String(),
		Nonce:     int64(tx.Nonce),
		Receiver:  string(tx.Key),
		Value:     string(tx.Value),
		GasLimit:  0,
		GasUsed:   deliverResult.GasUsed,
		GasPrice:  "1",
		Memo:      fmt.Sprintf("param set name:%s value:%s", tx.Key, tx.Value),
		Payload:   "",
		Events:    "",
		Codei:     deliverResult.Code,
		Codes:     deliverResult.Log,
		CreatedAt: block.Time,
	}
	ledger.GasLimit += int64(0)
	ledger.GasUsed += deliverResult.GasUsed
	ledger.TotalPrice = new(big.Int).Add(ledger.TotalPrice, new(big.Int).SetUint64(1))

	return trans, nil
}

func txTagToTypei(txTag []byte) int {
	typei := binary.LittleEndian.Uint16(txTag)
	return int(typei)
}

func (cli *Client) resolveTxEvents(tx *database.V3Transaction) ([]database.V3Payment, error) {
	var (
		logs     []*ethtypes.Log
		payments []database.V3Payment
	)
	if err := json.Unmarshal([]byte(tx.Events), &logs); err != nil {
		log.Logger.Error("Unmarshal events", zap.Error(err), zap.Any("event", tx.Events))
		return payments, err
	}

	for _, v := range logs {
		evname, indexed, unindexed, err := unpackEventV2(cli.abi, v)
		if err != nil {
			continue // 如果用此abi无法解析事件，则跳过继续解析后续的事件
		}

		var symbol string
		if token, ok := cli.tokens[v.Address.Hex()]; ok {
			symbol = token.Symbol
		}

		payment := database.V3Payment{
			Hash:      tx.Hash,
			Height:    tx.Height,
			EvName:    evname,
			Idx:       v.Index, // EVM的取事件的索引id
			Symbol:    symbol,
			Contract:  v.Address.Hex(),
			CreatedAt: tx.CreatedAt,
		}

		if evname == "Transfer" {
			payment.Sender = hashToAddress(indexed[0]).Hex()
			payment.Receiver = hashToAddress(indexed[1]).Hex()
			value, _ := unindexed[0].(*big.Int)
			payment.Value = value.String()
		} else if evname == "Deposit" {
			payment.Sender = tx.Sender
			payment.Receiver = hashToAddress(indexed[0]).Hex()
			value, _ := unindexed[0].(*big.Int)
			payment.Value = value.String()
		} else if evname == "Withdrawal" {
			payment.Sender = hashToAddress(indexed[0]).Hex()
			payment.Receiver = tx.Sender
			value, _ := unindexed[0].(*big.Int)
			payment.Value = value.String()
		}
		payments = append(payments, payment)
	}
	return payments, nil
}

func unpackEventV2(abi abi.ABI, log *ethtypes.Log) (string, []common.Hash, []interface{}, error) {
	if len(log.Topics) == 0 {
		return "", nil, nil, errors.New("No Topic")
	}
	event, err := abi.EventByID(log.Topics[0])
	if err != nil {
		return "", nil, nil, err
	}

	// agrs.Unpack只会解析未index的
	args := event.Inputs
	retval, err := args.UnpackValues(log.Data)
	return event.Name, log.Topics[1:], retval, err
}

func hashToAddress(hx common.Hash) common.Address {
	a := common.Address{}
	a.SetBytes(hx.Bytes())
	return a
}
