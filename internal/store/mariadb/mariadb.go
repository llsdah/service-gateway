package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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

// main repo
func (r *repository) FindRequestData(ctx context.Context, inputData model.RequestData) (model.RequestData, error) {

	const q = `SELECT API_CD, API_GROUP_CD, TARGET_URI FROM SID_API_DTL_MNG WHERE API_PATH = ? AND USG_YN = 'Y' AND API_TYP_CD = '00' LIMIT 1`

	err := r.db.QueryRowContext(ctx, q, inputData.RequestURL).Scan(
		&inputData.ApiCode,
		&inputData.ApiGroupCode,
		&inputData.RequestHost,
	)
	/**
	if err == sql.ErrNoRows {
		return inputData, nil // 없을 경우 빈 문자열
	}
	*/
	if err != nil {
		return inputData, err
	}

	return inputData, nil
}

// main repo
func (r *repository) ExistAPI(ctx context.Context, inputData model.RequestData) (bool, error) {

	var ClotCtlCd, ClotUablStaTim, ClotUablEndTim sql.NullString
	// ApiClotCtlCd: "00" 정상, "01" 시스템장애, "02" 거래량폭주, "03" 시스템점검, "04" 인터페이스장애, "05" 응답시간장애, "06" 오류누적, "07" 업무점검, "08" 지정기간거래불가(기간)
	// ApiClotUablStaTim, ApiClotUablEndTim: HHMMSS (지정기간거래불가 시 사용)
	// 비즈니스 로직에서 처리
	// 필요시 model.RequestData에 필드 추가
	const q = `SELECT API_CD, API_GROUP_CD, API_CLOT_CTL_CD, API_CLOT_UABL_STA_TIM, API_CLOT_UABL_END_TIM FROM SID_API_DTL_MNG WHERE API_PATH = ? AND USG_YN = 'Y' AND API_TYP_CD = '00' LIMIT 1`

	err := r.db.QueryRowContext(ctx, q, inputData.RequestURL).Scan(
		&inputData.ApiCode,
		&inputData.ApiGroupCode,
		&ClotCtlCd,
		&ClotUablStaTim,
		&ClotUablEndTim,
	)

	if err == sql.ErrNoRows {
		return false, nil // 없을 경우 빈 문자열
	}
	if err != nil {
		return false, err
	}

	// sql.NullString을 string으로 변환
	ctlCd := ""
	staTim := ""
	endTim := ""
	if ClotCtlCd.Valid {
		ctlCd = ClotCtlCd.String
	}
	if ClotUablStaTim.Valid {
		staTim = ClotUablStaTim.String
	}
	if ClotUablEndTim.Valid {
		endTim = ClotUablEndTim.String
	}

	// 제어 코드 체크
	controlErr := r.checkControlCodes(&ctlCd, &staTim, &endTim)

	if controlErr != nil {
		return false, controlErr
	}

	return true, nil
}

func (r *repository) ExistAPIGroup(ctx context.Context, inputData model.RequestData) (bool, error) {
	var ClotCtlCd, ClotUablStaTim, ClotUablEndTim sql.NullString

	const q = `SELECT API_GROUP_CLOT_CTL_CD, API_GROUP_CLOT_UABL_STA_TIM, API_GROUP_CLOT_UABL_END_TIM FROM SID_API_GRP_MNG WHERE API_GROUP_CD = ? AND USG_YN = 'Y'`

	err := r.db.QueryRowContext(ctx, q, inputData.ApiGroupCode).Scan(
		&ClotCtlCd,
		&ClotUablStaTim,
		&ClotUablEndTim,
	)
	if err == sql.ErrNoRows {
		return false, nil // 없을 경우 빈 문자열
	}
	if err != nil {
		return false, err
	}

	// sql.NullString을 string으로 변환
	ctlCd := ""
	staTim := ""
	endTim := ""
	if ClotCtlCd.Valid {
		ctlCd = ClotCtlCd.String
	}
	if ClotUablStaTim.Valid {
		staTim = ClotUablStaTim.String
	}
	if ClotUablEndTim.Valid {
		endTim = ClotUablEndTim.String
	}

	// 제어 코드 체크
	controlErr := r.checkControlCodes(&ctlCd, &staTim, &endTim)
	if controlErr != nil {
		return false, controlErr
	}

	return true, nil

}

func (r *repository) ExistUseAPIList(ctx context.Context, inputData model.RequestData) (bool, error) {
	const q = `SELECT EXISTS ( SELECT 1 FROM SID_BIZ_SRVC_API_RLP WHERE API_GROUP_CD = ? AND API_CD = ? AND BIZ_SRVC_CD = ? AND USG_YN = 'Y' )`
	var exists int
	if err := r.db.QueryRowContext(ctx, q, inputData.ApiGroupCode, inputData.ApiCode, inputData.BizServiceCode).Scan(&exists); err != nil {
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

func (m *mockRepository) ExistAPI(ctx context.Context, inputData model.RequestData) (bool, error) {
	return true, nil
}

func (m *mockRepository) ExistConfig(ctx context.Context, config string) (bool, error) {
	return true, nil
}

func (m *mockRepository) ExistUseAPIList(ctx context.Context, inputData model.RequestData) (bool, error) {
	return true, nil
}

func (m *mockRepository) Close() error {
	return nil
}

// 제어코드 비즈니스 로직
func (r *repository) checkControlCodes(ClotCtlCd, ClotUablStaTim, ClotUablEndTim *string) error {
	if *ClotCtlCd != "00" {
		// 08: 지정기간거래불가
		if *ClotCtlCd == "08" {
			now := time.Now().Format("20060102150405000") // HHMMSS
			if *ClotUablStaTim != "" && *ClotUablEndTim != "" {
				if now >= *ClotUablStaTim && now <= *ClotUablEndTim {
					return fmt.Errorf("%s", model.ErrorCodeMap[*ClotCtlCd])
				}
			}
		} else {
			// 00, 08이 아닌 경우 에러 반환
			msg := model.ErrorCodeMap[*ClotCtlCd]
			if msg == "" {
				msg = "알 수 없는 에러"
			}
			log.Printf("Control Code Error: [%s] %s ", *ClotCtlCd, msg)
			return fmt.Errorf("%s", msg)
		}
	}

	return nil
}
