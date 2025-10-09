package gateway

import (
    "encoding/json"

    "github.com/go-playground/validator/v10"
)

// JSONValidator는 T 타입으로 JSON을 언마샬하고 validator 태그로 필드 검증을 수행합니다.
func JSONValidator[T any]() func([]byte) error {
    v := validator.New()
    return func(raw []byte) error {
        var t T
        if len(raw) == 0 {
            return nil
        }
        if err := json.Unmarshal(raw, &t); err != nil {
            return err
        }
        return v.Struct(t)
    }
}