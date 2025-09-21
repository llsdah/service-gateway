* 해야 할것 
- api 통신 구조 만들기 : 
- 요청 들어온 url 로 전달 
- 요청 들어온 url 매핑 후 전달, 


1. 해야할 것은 마리아 DB의 API_PATH를 읽어서 1대1이 되었던 뭐가 되었건 실행하기 
  - 완료 
  - 고민해야될 부분

2. user 정보에 대한 필드 추가만 해야하는 내용 확인 
  - 완료
    - 임의 Session 필드 생성 값이 없다면 UUID로 생성 후 session-service 데이터 적재 -> 값입력
  - 고민해야될 부분
    - find GET으로 변경 요청은 POST
3. http 헤더 생성 및 조회 전달. 
  - 진행중
  - 고민해야될 부분

* Header 정의 
-> 추가내용이 있다면 확인 

- TCID : 거래 추척 , 년월일 (8자리) + hostname(8자리) + 거래시분초(8자리)  + 임의번호(8자리 숫자+ 소문자)
- TCIDSRNO : 4자리 0001
- BizSrvcCd
- BizSrvcIp 
- idempotency-key : x-fw-header에 데이터존재시 이중거래 G/W 체크, 없다면 미체크 
- fw_AUTHORIZATION : x-fw-header에 데이터존재시 인증 ENC 내의 코드  : 


* 별도 생성헤더 
- UUID 값 

* 마리아DB, Oracle
- FW URL : SID_API_DTL_MNG, 컬럼명 : API_GROUP_CD, API_CD, API_PATH, USG_YN 
- FW URL GROUP : SID_API_GRP_MNG, 컬럼명 : API_GROUP_CD, USG_YN
- 설정테이블 : SID_API_EST_MNG, 컬럼명 : API_GROUP_CD , USG_YN, API_EST_KEY, API_EST_VAL
- 업무서비스 코드 : SID_CMN_SRVC_MNG 컬럼명 : CMN_SRVC_CD


- 해당 시스템에서 허용되는 FW URL GROUP ->
- FW URL GROUP : SID_BIZ_SRVC_API_RLP, 컬럼명 : BIZ_SRVC_CD, API_CD, USG_YN




* 문제 특이 사항 
1. hostname 아는 문제 
- 현재 API 가 body에 입력됩 해당 데이터가 아닌 경우 매칭이 힘듬 
-> yaml 에서 서비스 명칭을 가지고 오는 부분이 필요 단 테이블의 url은 키가 되어야 합니다. 


* DB 조회
service-gateway (API_GOURP_CD == 006 ) 에서는 
SID_API_DTL_MNG -> API_PATH ( USG_YN = 'Y' && API_TYP_CD = '00' ) 로만 조회 ->
SID_API_GRP_MNG ( USG_YN = 'Y')  API_GROUP_CD YN 여부 확인 true 인것 만 시작 
API_GROUP_CD 으로 HOST mapping 시킨다 



log적재 관련 kafka 연결 
SID_API_EST_MNG (API_GOURP_CD == 006 & USG_YN = 'Y') 조회 

log.source.in
log.target.out
log.source.out
log.target.in

json format 

{
   "timeStamp" : "",
   "tcId":"",
   "tcIdSrno":1, #int
   "bizSrvcCd":"",
   "bizSrvcIp":"",
   "rasTyp":"11",
   "nmlYn":"Y",
   "apiPath":"/sid/",
   "cmmSrvcCd":"001",
   "cmmBizSrvcCd":"00001",
   "cmmBizSrvcSrno":1, #int
   "apiGroupCd":"001",
   "apiCd":"00001",
   "guid":"",
   "interId":"",
   "messageCd":"",
   "hostName":"",
   "data": {}
}
