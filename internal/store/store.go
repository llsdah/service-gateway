package store

import (
	"context"
	"service-gateway/internal/model"
)

/**
// 트랜잭션 묶음
type TxRepository interface {
	FindRequestData(ctx context.Context, inputData model.RequestData) (model.RequestData, error)
	ExistAPIGroup(ctx context.Context, inputData model.RequestData) (bool, error)
}
*/

type Repository interface {
	//WithTx(ctx context.Context, options *sql.TxOptions, fn func(TxRepository) error) error
	FindRequestData(ctx context.Context, inputData model.RequestData) (model.RequestData, error)
	ExistUseAPIList(ctx context.Context, inputData model.RequestData) (bool, error)
	ExistAPIGroup(ctx context.Context, inputData model.RequestData) (bool, error)
	ExistAPI(ctx context.Context, inputData model.RequestData) (bool, error)
	ExistConfig(ctx context.Context, config string) (bool, error)
	Close() error
}
