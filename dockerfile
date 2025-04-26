# 빌드 스테이지
FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# ✅ 리눅스용 바이너리로 빌드 (절대 필수) - 정적파일로 생성 필요 
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o service-gateway main.go

# 디버깅용 실행 이미지
# FROM alpine:latest

# ✅ bash, file 유틸리티 설치
# RUN apk add --no-cache bash file

# 빌드된 실행파일 복사
#COPY --from=builder /app/service-gateway /service-gateway

# 실행 권한 부여
#RUN chmod +x /service-gateway

# 기본 쉘 진입 가능하게 설정
# ENTRYPOINT ["/bin/bash"]

FROM gcr.io/distroless/static

COPY --from=builder /app/service-gateway /service-gateway

ENTRYPOINT ["/service-gateway"]
