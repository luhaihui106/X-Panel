package service

import "github.com/google/uuid"

// A handler-local receiver shares service dependencies and the delete tracker,
// never the draft of another actor. No lock/WaitGroup is copied.
func (t *Tgbot) withDraft(draft *telegramClientDraft) *Tgbot {
	owner := t
	if t.deleteOwner != nil {
		owner = t.deleteOwner
	}
	return &Tgbot{draft: draft, deleteOwner: owner, effects: t.effects, inboundService: t.inboundService, settingService: t.settingService, serverService: t.serverService, xrayService: t.xrayService, lastStatus: t.lastStatus}
}

func (t *Tgbot) initializeClientDraft() {
	*t.draft = telegramClientDraft{client_Id: uuid.NewString(), client_Email: t.randomLowerAndNum(8), client_Enable: true, client_SubID: t.randomLowerAndNum(16), client_Security: "auto", client_ShPassword: t.randomShadowSocksPassword(), client_TrPassword: t.randomLowerAndNum(10)}
}
