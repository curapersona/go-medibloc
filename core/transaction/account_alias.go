package transaction

import (
	"unicode"

	"github.com/gogo/protobuf/proto"
	"github.com/medibloc/go-medibloc/common"
	"github.com/medibloc/go-medibloc/core"
	corepb "github.com/medibloc/go-medibloc/core/pb"
	coreState "github.com/medibloc/go-medibloc/core/state"
	"github.com/medibloc/go-medibloc/util"
)

// RegisterAliasPayload is payload type for register alias
type RegisterAliasPayload struct {
	AliasName string
}

// FromBytes converts bytes to payload.
func (payload *RegisterAliasPayload) FromBytes(b []byte) error {
	payloadPb := &corepb.RegisterAliasPayload{}
	if err := proto.Unmarshal(b, payloadPb); err != nil {
		return err
	}
	payload.AliasName = payloadPb.AliasName
	return nil
}

// ToBytes returns marshaled DefaultPayload
func (payload *RegisterAliasPayload) ToBytes() ([]byte, error) {
	payloadPb := &corepb.RegisterAliasPayload{
		AliasName: payload.AliasName,
	}
	return proto.Marshal(payloadPb)
}

// RegisterAliasTx is a structure for register alias
type RegisterAliasTx struct {
	addr       common.Address
	collateral *util.Uint128
	alias      string
	size       int
}

var _ core.ExecutableTx = &RegisterAliasTx{}

//NewRegisterAliasTx returns RegisterAliasTx
func NewRegisterAliasTx(tx *coreState.Transaction) (core.ExecutableTx, error) {
	if len(tx.Payload()) > MaxPayloadSize {
		return nil, ErrTooLargePayload
	}
	payload := new(RegisterAliasPayload)
	if err := BytesToTransactionPayload(tx.Payload(), payload); err != nil {
		return nil, err
	}
	size, err := tx.Size()
	if err != nil {
		return nil, err
	}
	if !common.IsHexAddress(tx.From().Hex()) {
		return nil, ErrInvalidAddress
	}
	if err := validateAlias(payload.AliasName); err != nil {
		return nil, err
	}

	return &RegisterAliasTx{
		addr:       tx.From(),
		alias:      payload.AliasName,
		collateral: tx.Value(),
		size:       size,
	}, nil
}

//Execute RegisterAliasTx
func (tx *RegisterAliasTx) Execute(bs *core.BlockState) error {
	collateralLimit, err := util.NewUint128FromString(AliasCollateralMinimum)
	if err != nil {
		return err
	}
	if tx.collateral.Cmp(collateralLimit) < 0 {
		return ErrAliasCollateralLimit
	}

	acc, err := bs.GetAccount(tx.addr)
	if err != nil {
		return err
	}

	aliasBytes, err := acc.GetData("", []byte(coreState.AliasKey))
	if err != nil && err != ErrNotFound {
		return err
	}
	if aliasBytes != nil {
		return ErrAliasAlreadyHave
	}

	_, err = bs.GetAccountByAlias(tx.alias)
	if err != nil && err != ErrNotFound {
		return err
	} else if err == nil {
		return ErrAliasAlreadyTaken
	}

	acc.Balance, err = acc.Balance.Sub(tx.collateral)
	if err == util.ErrUint128Underflow {
		return ErrBalanceNotEnough
	}
	if err != nil {
		return err
	}

	collateralBytes, err := tx.collateral.ToFixedSizeByteSlice()
	if err != nil {
		return err
	}
	pbAlias := &corepb.Alias{
		AliasName:       tx.alias,
		AliasCollateral: collateralBytes,
	}
	aliasBytes, err = proto.Marshal(pbAlias)
	if err != nil {
		return err
	}
	err = acc.Data.Prepare()
	if err != nil {
		return err
	}
	err = acc.Data.BeginBatch()
	if err != nil {
		return err
	}
	err = acc.PutData("", []byte(coreState.AliasKey), aliasBytes)
	if err != nil {
		return err
	}
	err = acc.Data.Commit()
	if err != nil {
		return err
	}
	err = acc.Data.Flush()
	if err != nil {
		return err
	}
	err = bs.PutAccount(acc)
	if err != nil {
		return err
	}

	return bs.PutAccountAlias(tx.alias, tx.addr)
}

//Bandwidth returns bandwidth.
func (tx *RegisterAliasTx) Bandwidth() *common.Bandwidth {
	return common.NewBandwidth(1500, uint64(tx.size))
}

func (tx *RegisterAliasTx) PointModifier(points *util.Uint128) (modifiedPoints *util.Uint128, err error) {
	return points, nil
}

// DeregisterAliasTx is a structure for deregister alias
type DeregisterAliasTx struct {
	addr common.Address
	size int
}

var _ core.ExecutableTx = &DeregisterAliasTx{}

//NewDeregisterAliasTx returns RegisterAliasTx
func NewDeregisterAliasTx(tx *coreState.Transaction) (core.ExecutableTx, error) {
	if len(tx.Payload()) > MaxPayloadSize {
		return nil, ErrTooLargePayload
	}
	size, err := tx.Size()
	if err != nil {
		return nil, err
	}
	if !common.IsHexAddress(tx.From().Hex()) {
		return nil, ErrInvalidAddress
	}

	return &DeregisterAliasTx{
		addr: tx.From(),
		size: size,
	}, nil
}

//Execute DeregisterAliasTx
func (tx *DeregisterAliasTx) Execute(bs *core.BlockState) error {
	acc, err := bs.GetAccount(tx.addr)
	if err != nil {
		return err
	}

	aliasBytes, err := acc.GetData("", []byte(coreState.AliasKey))
	if err != nil && err != ErrNotFound {
		return err
	}
	if err == ErrNotFound || aliasBytes == nil {
		return ErrAliasNotExist
	}

	pbAlias := new(corepb.Alias)
	err = proto.Unmarshal(aliasBytes, pbAlias)
	if err != nil {
		return err
	}
	if pbAlias.AliasName == "" {
		return ErrAliasNotExist
	}
	collateral, err := util.NewUint128FromFixedSizeByteSlice(pbAlias.AliasCollateral)
	if err != nil {
		return err
	}
	acc.Balance, err = acc.Balance.Add(collateral)
	if err != nil {
		return err
	}

	err = bs.DelAccountAlias(pbAlias.AliasName, tx.addr)
	if err != nil {
		return err
	}

	err = acc.Data.Prepare()
	if err != nil {
		return err
	}
	err = acc.Data.BeginBatch()
	if err != nil {
		return err
	}
	err = acc.Data.Delete([]byte(coreState.AliasKey))
	if err != nil {
		return err
	}
	err = acc.Data.Commit()
	if err != nil {
		return err
	}
	err = acc.Data.Flush()
	if err != nil {
		return err
	}
	return bs.PutAccount(acc)
}

//Bandwidth returns bandwidth.
func (tx *DeregisterAliasTx) Bandwidth() *common.Bandwidth {
	return common.NewBandwidth(1500, uint64(tx.size))
}

// PointModifier returns modifier
func (tx *DeregisterAliasTx) PointModifier(points *util.Uint128) (modifiedPoints *util.Uint128, err error) {
	return points, nil
}

// ValidateAlias checks alias
func validateAlias(alias string) error {
	if len(alias) < AliasLengthMinimum {
		return ErrAliasLengthUnderMinimum
	}
	if len(alias) > AliasLengthMaximum {
		return ErrAliasLengthExceedMaximum
	}
	for i := 0; i < len(alias); i++ {
		ch := rune(alias[i])

		if !(unicode.IsNumber(ch) || unicode.IsLower(ch)) {
			return ErrAliasInvalidChar
		}
		if i == 0 && unicode.IsNumber(ch) {
			return ErrAliasFirstLetter
		}
	}
	return nil
}