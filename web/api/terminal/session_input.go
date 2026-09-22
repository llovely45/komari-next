package terminal

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/web/api"
)

// MaxTerminalInputBytes limits one generic terminal input frame. Plugins may
// send arbitrary terminal data, so this API deliberately does not interpret
// it as a shell command or add a line ending.
const MaxTerminalInputBytes = 1 << 20

type adminTerminalSession struct {
	RequestID  string `json:"request_id"`
	UUID       string `json:"uuid"`
	ClientName string `json:"client_name"`
}

// ListAdminTerminalSessions returns the caller's live terminal sessions. The
// request ID is needed by generic terminal extensions when writing input.
func ListAdminTerminalSessions(c *gin.Context) {
	userUUID, ok := currentAdminUUID(c)
	if !ok {
		api.RespondError(c, http.StatusUnauthorized, "An administrator session is required")
		return
	}

	active := ActiveSessionsForUser(userUUID)
	result := make([]adminTerminalSession, 0, len(active))
	for _, session := range active {
		item := adminTerminalSession{RequestID: session.RequestID, UUID: session.UUID}
		if client, err := clients.GetClientByUUID(session.UUID); err == nil {
			item.ClientName = client.Name
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ClientName != result[j].ClientName {
			return result[i].ClientName < result[j].ClientName
		}
		if result[i].UUID != result[j].UUID {
			return result[i].UUID < result[j].UUID
		}
		return result[i].RequestID < result[j].RequestID
	})
	api.RespondSuccess(c, result)
}

type terminalInputRequest struct {
	Data string `json:"data"`
}

// WriteAdminTerminalInput forwards one raw input frame to a live terminal
// session owned by the authenticated administrator. Command-specific behavior
// such as adding Enter remains in the calling plugin.
func WriteAdminTerminalInput(c *gin.Context) {
	userUUID, ok := currentAdminUUID(c)
	if !ok {
		api.RespondError(c, http.StatusUnauthorized, "An administrator session is required")
		return
	}

	// JSON may encode every input byte as a six-character escape (for example,
	// a NUL byte), so allow the worst-case JSON expansion before enforcing the
	// decoded one-MiB frame limit below.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxTerminalInputBytes*6+1024)
	var request terminalInputRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			api.RespondError(c, http.StatusRequestEntityTooLarge, "Terminal input request body is too large")
			return
		}
		api.RespondError(c, http.StatusBadRequest, "Invalid terminal input request")
		return
	}
	if len(request.Data) == 0 {
		api.RespondError(c, http.StatusBadRequest, "Terminal input data is required")
		return
	}
	if len(request.Data) > MaxTerminalInputBytes {
		api.RespondError(c, http.StatusRequestEntityTooLarge, "Terminal input exceeds the 1 MiB limit")
		return
	}

	requestID := c.Param("request_id")
	uuid, err := WriteSessionInput(requestID, userUUID, []byte(request.Data))
	switch {
	case errors.Is(err, ErrSessionNotFound):
		api.RespondError(c, http.StatusNotFound, "Terminal session not found")
		return
	case errors.Is(err, ErrSessionNotActive):
		api.RespondError(c, http.StatusConflict, "Terminal session is not connected")
		return
	case err != nil:
		api.RespondError(c, http.StatusInternalServerError, "Failed to write terminal input")
		return
	}

	actor, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), fmt.Sprint(actor), fmt.Sprintf("terminal input sent: uuid=%s request_id=%s bytes=%d", uuid, requestID, len(request.Data)), "terminal")
	api.RespondSuccess(c, nil)
}

func currentAdminUUID(c *gin.Context) (string, bool) {
	value, exists := c.Get("uuid")
	if !exists {
		return "", false
	}
	userUUID, ok := value.(string)
	return userUUID, ok && userUUID != "" && userUUID != "00000000-0000-0000-0000-000000000000"
}
