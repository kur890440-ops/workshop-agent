package auth

import "workshop-agent/internal/workshops"

type Service struct { ws *workshops.Service }

func NewService(ws *workshops.Service) *Service { return &Service{ws: ws} }

func (s *Service) ResolveWorkshop(telegramChatID int64) (int64, bool, error) {
	if telegramChatID == 0 {
		return 0, false, nil
	}
	id, err := s.ws.GetWorkshopByChat(telegramChatID)
	if err != nil { return 0, false, err }
	return id, id > 0, nil
}
