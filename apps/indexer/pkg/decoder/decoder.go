package decoder

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/core/types"
)

// UniversalDecoder는 임의의 ABI를 해석하여 동적으로 JSON 해시맵으로 변환합니다.
type UniversalDecoder struct {
	contractABI abi.ABI
	// Topic[0] (Keccak256 해시)를 키로 하여 이벤트 구조체에 O(1)로 접근하기 위한 라우터
	eventRouter map[string]abi.Event
}

// NewUniversalDecoder는 JSON 형태의 ABI 문자열을 받아 디코더 싱글톤을 초기화합니다.
func NewUniversalDecoder(abiJSON string) (*UniversalDecoder, error) {
	parsedABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return nil, fmt.Errorf("ABI 파싱 실패: %w", err)
	}

	router := make(map[string]abi.Event)
	for _, event := range parsedABI.Events {
		router[event.ID.Hex()] = event
	}

	return &UniversalDecoder{
		contractABI: parsedABI,
		eventRouter: router,
	}, nil
}

// DecodeEvent는 EVM 로그를 받아 인덱스/비인덱스 파라미터를 통합한 map을 반환합니다.
func (d *UniversalDecoder) DecodeEvent(log types.Log) (string, map[string]interface{}, error) {
	if len(log.Topics) == 0 {
		return "", nil, fmt.Errorf("토픽이 없는 빈 로그입니다")
	}

	// 1. Topic[0]를 이용하여 해당하는 이벤트를 O(1)로 찾아냅니다.
	signature := log.Topics[0].Hex()
	event, exists := d.eventRouter[signature]
	if !exists {
		return "", nil, fmt.Errorf("등록된 ABI에서 시그니처(%s)를 찾을 수 없습니다", signature)
	}

	// 2. 비인덱스 파라미터 언패킹 (Data 영역)
	decodedData := make(map[string]interface{})
	if len(log.Data) > 0 {
		err := event.Inputs.UnpackIntoMap(decodedData, log.Data)
		if err != nil {
			return event.Name, nil, fmt.Errorf("비인덱스 데이터 언패킹 실패: %w", err)
		}
	}

	// 3. 인덱스 파라미터 병합 (Topics 영역)
	var indexedArgs abi.Arguments
	for _, arg := range event.Inputs {
		if arg.Indexed {
			indexedArgs = append(indexedArgs, arg)
		}
	}

	// 첫 번째 토픽은 시그니처이므로 제외하고 넘깁니다.
	if len(indexedArgs) > 0 && len(log.Topics) > 1 {
		err := abi.ParseTopicsIntoMap(decodedData, indexedArgs, log.Topics[1:])
		if err != nil {
			return event.Name, nil, fmt.Errorf("인덱스 데이터 파싱 실패: %w", err)
		}
	}

	// 4. 거대 정수 손실 방지 및 바이트 배열 정규화 (Sanitization)
	sanitizeMap(decodedData)

	return event.Name, decodedData, nil
}

// sanitizeMap은 재귀적으로 맵을 순회하며 DB와 JSON에 안전한 타입으로 포맷팅합니다.
func sanitizeMap(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case *big.Int:
			m[k] = val.String() // 거대 정수를 문자열로 변환 (손실 방지)
		case []byte:
			m[k] = "0x" + hex.EncodeToString(val) // 바이트 배열을 0x Hex로 변환
		case [32]byte:
			m[k] = "0x" + hex.EncodeToString(val[:])
		case map[string]interface{}:
			sanitizeMap(val) // 중첩 구조체(Tuple) 재귀 처리
		}
	}
}
