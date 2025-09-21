package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	config "service-gateway/internal/configs"
	"service-gateway/internal/model"
	"service-gateway/internal/store"
	"time"

	// ★ 이 줄이 반드시 있어야 함
	_ "github.com/go-sql-driver/mysql"
)

type Config struct {
	Enabled  bool
	User     string
	Password string
	Host     string
	Port     int
	DBName   string
}

type repository struct {
	db *sql.DB
}

type mockRepository struct{}

func New(cfg Config) (store.Repository, error) {

	if !cfg.Enabled {
		return &mockRepository{}, nil
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&charset=utf8mb4,utf8",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return &repository{db: db}, nil
}

/**
type txRepository struct{ tx *sql.Tx }

	func (r *repository) WithTx(ctx context.Context, options *sql.TxOptions, fn func(store.TxRepository) error) error {
		tx, err := r.db.BeginTx(ctx, options) // 단일 커넥션
		if err != nil {
			return err
		}
		txRepo := &txRepository{
			tx: tx,
		}
		if err := fn(txRepo); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
*/

// main repo
func (r *repository) FindRequestData(ctx context.Context, inputData model.RequestData) (model.RequestData, error) {
	const q = `SELECT API_CD, API_GROUP_CD FROM SID_API_DTL_MNG WHERE API_PATH = ? AND USG_YN = 'Y' AND API_TYP_CD = '00' LIMIT 1`

	err := r.db.QueryRowContext(ctx, q, inputData.RequestURL).Scan(&inputData.ApiCode, &inputData.ApiGroupCode)

	if err == sql.ErrNoRows {
		return inputData, nil // 없을 경우 빈 문자열
	}
	if err != nil {
		return inputData, err
	}

	return inputData, nil
}

// 실제 테이블/컬럼명으로 바꿔 쓰세요(예시는 SERVICE_HOST_MAP).
func (r *repository) ExistAPIGroup(ctx context.Context, inputData model.RequestData) (bool, error) {
	const q = `SELECT EXISTS ( SELECT 1 FROM SID_API_GRP_MNG WHERE API_GROUP_CD = ? AND USG_YN = 'Y')`
	var exists int
	if err := r.db.QueryRowContext(ctx, q, inputData.ApiGroupCode).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (r *repository) ExistConfig(ctx context.Context, configKey string) (bool, error) {

	const q = `SELECT EXISTS ( SELECT 1 FROM SID_API_EST_MNG WHERE API_GROUP_CD = ? AND VALUE = ? AND USG_YN = 'Y')`
	var exists int
	if err := r.db.QueryRowContext(ctx, q, config.AppConfig.Application.GroupCode, configKey).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (r *repository) Close() error {
	return r.db.Close()
}

// mockRepo
func (m *mockRepository) FindRequestData(ctx context.Context, inputData model.RequestData) (model.RequestData, error) {
	inputData.ApiCode = "006"
	return inputData, nil
}

func (m *mockRepository) ExistAPIGroup(ctx context.Context, inputData model.RequestData) (bool, error) {
	return true, nil
}

func (m *mockRepository) ExistConfig(ctx context.Context, config string) (bool, error) {
	return true, nil
}

func (m *mockRepository) Close() error {
	return nil
}
