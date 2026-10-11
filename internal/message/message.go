// Package message defines the application input/output boundary shared by transports.
package message

type Inbound struct {
	WorkshopID                                           int64
	InputType                                            string
	UserID, TelegramUserID, ConversationID, WebSessionID int64
	RequestID, Text, ConfirmationID                      string
}

type Button struct {
	Text string `json:"text"`
	ID   string `json:"id"`
}
type Output struct {
	WorkshopID int64    `json:"-"`
	RequestID  string   `json:"request_id,omitempty"`
	ID         int64    `json:"id"`
	Role       string   `json:"role"`
	Text       string   `json:"text"`
	Buttons    []Button `json:"buttons,omitempty"`
}
