package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"AetherGrid/controlPlane/internal/domain"
	"AetherGrid/controlPlane/internal/service"

	"github.com/google/uuid"
)

// CommandHandler exposes the command queue used to dispatch instructions to
// edge node agents.
type CommandHandler struct {
	commands *service.CommandService
	logger   *log.Logger
}

// NewCommandHandler constructs a CommandHandler with the given service.
func NewCommandHandler(commands *service.CommandService, logger *log.Logger) *CommandHandler {
	return &CommandHandler{commands: commands, logger: logger}
}

// Create handles POST /nodes/{id}/commands.
func (h *CommandHandler) Create(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	var request createCommandRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	command, err := h.commands.Create(r.Context(), service.CreateCommandInput{
		NodeID:     id,
		Type:       request.Type,
		Parameters: request.Parameters,
	})
	if err != nil {
		h.writeServiceError(w, err, "creating command")
		return
	}

	h.logger.Printf("command created: node_id=%s command_id=%s type=%s", command.NodeID, command.ID, command.Type)
	writeJSON(w, http.StatusCreated, newCommandResponse(command))
}

// List handles GET /nodes/{id}/commands?status=pending.
func (h *CommandHandler) List(w http.ResponseWriter, r *http.Request) {
	id, ok := nodeID(w, r)
	if !ok {
		return
	}

	status := domain.CommandStatus(strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status"))))

	commands, err := h.commands.ListByNode(r.Context(), id, status)
	if err != nil {
		h.writeServiceError(w, err, "listing commands")
		return
	}

	responses := make([]commandResponse, 0, len(commands))
	for _, command := range commands {
		responses = append(responses, newCommandResponse(command))
	}
	writeJSON(w, http.StatusOK, responses)
}

// ReportResult handles POST /nodes/{id}/commands/{command_id}/result.
func (h *CommandHandler) ReportResult(w http.ResponseWriter, r *http.Request) {
	node, ok := nodeID(w, r)
	if !ok {
		return
	}

	commandID := r.PathValue("command_id")
	if _, err := uuid.Parse(commandID); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid command id %q", commandID))
		return
	}

	var request reportCommandResultRequest
	if !decodeJSON(w, r, &request) {
		return
	}

	command, err := h.commands.ReportResult(r.Context(), service.ReportResultInput{
		NodeID:    node,
		CommandID: commandID,
		Status:    domain.CommandStatus(strings.ToUpper(strings.TrimSpace(request.Status))),
		Result:    request.Result,
		Error:     request.Error,
	})
	if err != nil {
		h.writeServiceError(w, err, "recording command result")
		return
	}

	h.logger.Printf("command result recorded: node_id=%s command_id=%s status=%s", node, command.ID, command.Status)
	writeJSON(w, http.StatusOK, newCommandResponse(command))
}

// writeServiceError maps service/repository errors to consistent HTTP
// responses. Internal errors are logged and never exposed to clients.
func (h *CommandHandler) writeServiceError(w http.ResponseWriter, err error, action string) {
	var validation *service.ValidationError
	switch {
	case errors.As(err, &validation):
		writeError(w, http.StatusBadRequest, validation.Message)
	case service.IsCommandNotFound(err):
		writeError(w, http.StatusNotFound, "command not found")
	case service.IsNotFound(err):
		writeError(w, http.StatusNotFound, "node not found")
	default:
		h.logger.Printf("%s: %v", action, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// createCommandRequest is the JSON payload for POST /nodes/{id}/commands.
type createCommandRequest struct {
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
}

// reportCommandResultRequest is the JSON payload for
// POST /nodes/{id}/commands/{command_id}/result.
type reportCommandResultRequest struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// commandResponse is the JSON representation of a command.
type commandResponse struct {
	ID         string          `json:"id"`
	NodeID     string          `json:"node_id"`
	Type       string          `json:"type"`
	Parameters json.RawMessage `json:"parameters"`
	Status     string          `json:"status"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

func newCommandResponse(command *domain.Command) commandResponse {
	return commandResponse{
		ID:         command.ID,
		NodeID:     command.NodeID,
		Type:       command.Type,
		Parameters: command.Parameters,
		Status:     string(command.Status),
		Result:     command.Result,
		Error:      command.Error,
		CreatedAt:  command.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:  command.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}
