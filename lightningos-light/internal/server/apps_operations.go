package server

import "time"

type appOperationState struct {
	appOperationInfo
	token uint64
}

func (s *Server) beginAppOperation(appID, action string) (func(), bool) {
	if s == nil || appID == "" || action == "" {
		return func() {}, false
	}
	s.appOperationsMu.Lock()
	defer s.appOperationsMu.Unlock()
	if s.appOperations == nil {
		s.appOperations = make(map[string]appOperationState)
	}
	if _, exists := s.appOperations[appID]; exists {
		return func() {}, false
	}
	s.appOperationSequence++
	token := s.appOperationSequence
	s.appOperations[appID] = appOperationState{
		appOperationInfo: appOperationInfo{Action: action, StartedAt: time.Now().UTC()},
		token:            token,
	}
	return func() { s.finishAppOperation(appID, token) }, true
}

func (s *Server) finishAppOperation(appID string, token uint64) {
	if s == nil {
		return
	}
	s.appOperationsMu.Lock()
	defer s.appOperationsMu.Unlock()
	operation, exists := s.appOperations[appID]
	if exists && operation.token == token {
		delete(s.appOperations, appID)
	}
}

func (s *Server) currentAppOperation(appID string) (appOperationInfo, bool) {
	if s == nil {
		return appOperationInfo{}, false
	}
	s.appOperationsMu.Lock()
	defer s.appOperationsMu.Unlock()
	operation, exists := s.appOperations[appID]
	return operation.appOperationInfo, exists
}

func (s *Server) updateAppOperationStage(appID, stage string) {
	if s == nil || appID == "" {
		return
	}
	s.appOperationsMu.Lock()
	defer s.appOperationsMu.Unlock()
	operation, exists := s.appOperations[appID]
	if !exists {
		return
	}
	operation.Stage = stage
	s.appOperations[appID] = operation
}

func (s *Server) appOperationSnapshot() map[string]appOperationInfo {
	operations := make(map[string]appOperationInfo)
	if s == nil {
		return operations
	}
	s.appOperationsMu.Lock()
	defer s.appOperationsMu.Unlock()
	for appID, operation := range s.appOperations {
		operations[appID] = operation.appOperationInfo
	}
	return operations
}
