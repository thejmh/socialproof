package handlers

import (
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thejmh/socialproof/apps/indexer/internal/engine"
)

type AdminHandler struct {
	db      *sql.DB
	engines map[string]*engine.IndexerEngine
	logger  *slog.Logger
}

func NewAdminHandler(db *sql.DB, engines map[string]*engine.IndexerEngine, logger *slog.Logger) *AdminHandler {
	return &AdminHandler{
		db:      db,
		engines: engines,
		logger:  logger,
	}
}

// GET /api/v1/admin/sync-failures
func (h *AdminHandler) GetSyncFailures(c *gin.Context) {
	query := `SELECT id, tx_hash, log_index, block_number, error_reason FROM failed_events WHERE status = 'PENDING' ORDER BY block_number DESC LIMIT 50`
	rows, err := h.db.QueryContext(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB 조회 실패"})
		return
	}
	defer rows.Close()

	var failures []map[string]interface{}
	for rows.Next() {
		var id, txHash, errorReason string
		var logIndex int
		var blockNum int64
		if err := rows.Scan(&id, &txHash, &logIndex, &blockNum, &errorReason); err == nil {
			failures = append(failures, map[string]interface{}{
				"id":           id,
				"tx_hash":      txHash,
				"log_index":    logIndex,
				"block_number": blockNum,
				"error_reason": errorReason,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"failures": failures})
}

// POST /api/v1/admin/reprocess/event/:id
func (h *AdminHandler) ReprocessEvent(c *gin.Context) {
	eventID := c.Param("id")
	contractName := c.Query("contract") // 쿼리로 타겟 엔진 지정 유도 (단일 엔진이면 생략 가능)

	// 만약 엔진을 명시하지 않았다면 첫번째 엔진으로 Fallback
	var targetEngine *engine.IndexerEngine
	if contractName != "" {
		targetEngine = h.engines[contractName]
	} else {
		for _, eng := range h.engines {
			targetEngine = eng
			break
		}
	}

	if targetEngine == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "대상 엔진을 찾을 수 없습니다"})
		return
	}

	if err := targetEngine.ProcessSingleLog(c.Request.Context(), eventID); err != nil {
		h.logger.Error("단건 재처리 실패", "event_id", eventID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "status": "failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Event reprocessed and resolved successfully", "status": "resolved"})
}

// POST /api/v1/admin/reprocess/block-range
func (h *AdminHandler) ReprocessBlockRange(c *gin.Context) {
	var payload struct {
		FromBlock int64  `json:"from_block" binding:"required"`
		ToBlock   int64  `json:"to_block" binding:"required"`
		Contract  string `json:"contract" binding:"required"`
	}

	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	targetEngine, exists := h.engines[payload.Contract]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "해당 컨트랙트 엔진이 가동 중이 아닙니다"})
		return
	}

	// 엔진의 TaskQueue로 재처리 구간 Push (비동기 Fire and Forget)
	targetEngine.ReprocessRange(c.Request.Context(), payload.FromBlock, payload.ToBlock)

	c.JSON(http.StatusAccepted, gin.H{
		"message":  "구간 재처리 작업이 백그라운드 큐에 적재되었습니다",
		"contract": payload.Contract,
		"from":     payload.FromBlock,
		"to":       payload.ToBlock,
	})
}
