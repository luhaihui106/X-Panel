package service

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
)

// Serialize handler/restore transitions, not administrator ownership.
var telegramInteractionMu sync.Mutex

type telegramWizard struct {
	until    time.Time
	messages map[int]bool
	draft    *telegramClientDraft
	inbound  *telegramInboundDraft
}

var wizardSessions = make(map[int64]*telegramWizard)
var bindingRequests = make(map[int64]struct {
	id     int32
	client int
	until  time.Time
})
var consumedCallbacks = make(map[string]time.Time)

type issuedCallback struct {
	actor   int64
	message int
	data    string
	until   time.Time
}

var issuedCallbacks = make(map[string]issuedCallback)
var callbackRegistryMu sync.Mutex

func secureKeyboard(actor int64, markup telego.ReplyMarkup) telego.ReplyMarkup {
	callbackRegistryMu.Lock()
	defer callbackRegistryMu.Unlock()
	keyboard, ok := markup.(*telego.InlineKeyboardMarkup)
	if !ok {
		return markup
	}
	copyKeyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: make([][]telego.InlineKeyboardButton, len(keyboard.InlineKeyboard))}
	for i, row := range keyboard.InlineKeyboard {
		copyKeyboard.InlineKeyboard[i] = append([]telego.InlineKeyboardButton(nil), row...)
		for j, button := range row {
			if button.CallbackData != "" {
				data := button.CallbackData
				if existing, ok := issuedCallbacks[data]; ok {
					data = existing.data
				} else if hashStorage != nil {
					if decoded, err := (&Tgbot{}).decodeQuery(data); err == nil {
						data = decoded
					}
				}
				token := "cb:" + uuid.NewString()
				issuedCallbacks[token] = issuedCallback{actor: actor, data: data, until: time.Now().Add(20 * time.Minute)}
				copyKeyboard.InlineKeyboard[i][j].CallbackData = token
			}
		}
	}
	return copyKeyboard
}
func bindKeyboard(actor int64, message int, markup telego.ReplyMarkup) {
	callbackRegistryMu.Lock()
	defer callbackRegistryMu.Unlock()
	keyboard, ok := markup.(*telego.InlineKeyboardMarkup)
	if !ok {
		return
	}
	for token, record := range issuedCallbacks {
		if record.actor == actor && record.message == message || !time.Now().Before(record.until) {
			delete(issuedCallbacks, token)
		}
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if record, ok := issuedCallbacks[button.CallbackData]; ok {
				record.message = message
				issuedCallbacks[button.CallbackData] = record
			}
		}
	}
	rememberWizardMessage(actor, message, markup)
}

func rememberWizardMessage(chatId int64, messageId int, markup telego.ReplyMarkup) {
	session := wizardSessions[chatId]
	if session == nil || !time.Now().Before(session.until) {
		return
	}
	keyboard, ok := markup.(*telego.InlineKeyboardMarkup)
	if !ok {
		return
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			data := button.CallbackData
			if issued, ok := issuedCallbacks[data]; ok {
				data = issued.data
			}
			if hashStorage != nil {
				if decoded, err := (&Tgbot{}).decodeQuery(data); err == nil {
					data = decoded
				}
			}
			if strings.HasPrefix(data, "add_client") || strings.HasPrefix(data, "add_inbound") {
				session.messages = map[int]bool{messageId: true}
				return
			}
		}
	}
}

func privateTelegramActor(chat telego.Chat, actor int64) bool {
	return actor > 0 && chat.Type == "private" && chat.ID == actor
}

func (t *Tgbot) denyCallback(q *telego.CallbackQuery) {
	if q != nil && q.ID != "" && bot != nil {
		t.sendCallbackAnswerTgBot(q.ID, "Access denied or button expired. Open a fresh private menu.")
	}
}

// Fail closed before *either* dispatch switch, independent of caller flags.
func (t *Tgbot) authorizeCallback(q *telego.CallbackQuery) (string, bool) {
	callbackRegistryMu.Lock()
	defer callbackRegistryMu.Unlock()
	if q == nil || q.Message == nil || !privateTelegramActor(q.Message.GetChat(), q.From.ID) {
		return "", false
	}
	message, ok := q.Message.(*telego.Message)
	if !ok || message.ReplyMarkup == nil || message.Date <= 0 {
		return "", false
	}
	now := time.Now()
	if message.Date > now.Unix()+30 {
		return "", false
	}
	present := false
	for _, row := range message.ReplyMarkup.InlineKeyboard {
		for _, button := range row {
			if button.CallbackData == q.Data {
				present = true
			}
		}
	}
	if !present {
		return "", false
	}
	issued, valid := issuedCallbacks[q.Data]
	if !valid || issued.actor != q.From.ID || issued.message != message.MessageID || !now.Before(issued.until) {
		return "", false
	}
	decoded := issued.data
	args := strings.Split(decoded, " ")
	if len(args) == 0 {
		return "", false
	}
	for _, arg := range args {
		if arg == "" {
			return "", false
		}
	}
	action := args[0]
	arity, known := callbackArity[action]
	if !known || len(args) < arity[0] || len(args) > arity[1] {
		return "", false
	}
	admin := checkAdmin(q.From.ID)
	// Numeric payloads are bounded before dispatch arithmetic or mutation.
	startNumeric := 0
	switch action {
	case "get_clients", "add_client_to", "add_client_limit_traffic_c", "add_client_limit_traffic_in", "add_client_reset_exp_c", "add_client_reset_exp_in", "add_client_ip_limit_c", "add_client_ip_limit_in":
		startNumeric = 1
	case "limit_traffic_c", "limit_traffic_in", "reset_exp_c", "reset_exp_in", "ip_limit_c", "ip_limit_in":
		startNumeric = 2
	}
	if startNumeric > 0 {
		for i := startNumeric; i < len(args); i++ {
			n, err := strconv.Atoi(args[i])
			if err != nil || n > 999999 || n < 0 && !(strings.HasSuffix(action, "_in") && i == len(args)-1 && len(args) == arity[1] && (n == -1 || n == -2)) {
				return "", false
			}
		}
	}
	if !admin {
		switch action {
		case "client_commands", "client_traffic":
		case "client_get_usage":
			if t.inboundService == nil {
				return "", false
			}
			_, client, err := t.inboundService.GetClientByEmail(args[1])
			if err != nil || client == nil || client.TgID != q.From.ID {
				return "", false
			}
		default:
			return "", false
		}
	}
	if strings.HasPrefix(action, "add_client") {
		session := wizardSessions[q.From.ID]
		if action != "add_client" && (session == nil || session.draft == nil || !now.Before(session.until) || !session.messages[message.MessageID]) {
			return "", false
		}
	}
	if strings.HasPrefix(action, "add_inbound") && action != "add_inbound" {
		session := wizardSessions[q.From.ID]
		if session == nil || session.inbound == nil || !now.Before(session.until) || !session.messages[message.MessageID] {
			return "", false
		}
	}
	key := q.ID
	if key == "" {
		return "", false
	}
	oneShot := action == "add_inbound" || action == "add_inbound_confirm" || action == "restart_core_confirm" || action == "reset_all_traffics_c" || action == "confirm_sub_install" || action == "restart_panel_confirm" || action == "add_client_submit_enable" || action == "add_client_submit_disable" || action == "reset_traffic_c" || action == "clear_ips_c" || action == "tgid_remove_c" || action == "toggle_enable_c" || action == "limit_traffic_c" || action == "reset_exp_c" || action == "ip_limit_c"
	mutationKey := strconv.FormatInt(q.From.ID, 10) + ":" + strconv.Itoa(message.MessageID) + ":" + q.Data
	if oneShot {
		if _, used := consumedCallbacks[mutationKey]; used {
			return "", false
		}
	}
	for key, expires := range consumedCallbacks {
		if !now.Before(expires) {
			delete(consumedCallbacks, key)
		}
	}
	if _, replay := consumedCallbacks[key]; replay {
		return "", false
	}
	consumedCallbacks[key] = now.Add(20 * time.Minute)
	if oneShot {
		consumedCallbacks[mutationKey] = now.Add(20 * time.Minute)
	}
	if strings.HasPrefix(action, "add_client") {
		if action == "add_client" {
			wizardSessions[q.From.ID] = &telegramWizard{draft: &telegramClientDraft{}, messages: make(map[int]bool)}
			delete(userStates, q.From.ID)
		}
		wizardSessions[q.From.ID].until = now.Add(20 * time.Minute)
	}
	return decoded, true
}

// Only implemented actions are routable. New handlers must opt in here.
var callbackArity = map[string][2]int{
	"add_inbound": {1, 1}, "add_inbound_vless": {1, 1}, "add_inbound_vmess": {1, 1}, "add_inbound_confirm": {1, 1}, "add_inbound_edit": {1, 1}, "add_inbound_cancel": {1, 1},
	"restart_core_confirm": {1, 1}, "restart_core_cancel": {1, 1},
	"menu_root": {1, 1}, "menu_status": {1, 1}, "menu_clients": {1, 1}, "menu_backup": {1, 1}, "menu_system": {1, 1},
	"client_get_usage": {2, 2}, "client_refresh": {2, 2}, "client_cancel": {2, 2},
	"ips_refresh": {2, 2}, "ips_cancel": {2, 2}, "tgid_refresh": {2, 2}, "tgid_cancel": {2, 2},
	"reset_traffic": {2, 2}, "reset_traffic_c": {2, 2}, "limit_traffic": {2, 2}, "limit_traffic_c": {3, 3}, "limit_traffic_in": {3, 4},
	"reset_exp": {2, 2}, "reset_exp_c": {3, 3}, "reset_exp_in": {3, 4}, "ip_limit": {2, 2}, "ip_limit_c": {3, 3}, "ip_limit_in": {3, 4},
	"clear_ips": {2, 2}, "clear_ips_c": {2, 2}, "ip_log": {2, 2}, "tg_user": {2, 2}, "tgid_remove": {2, 2}, "tgid_remove_c": {2, 2},
	"toggle_enable": {2, 2}, "toggle_enable_c": {2, 2}, "get_clients": {2, 2}, "add_client_to": {2, 2},
	"add_client_limit_traffic_c": {2, 2}, "add_client_limit_traffic_in": {2, 3}, "add_client_reset_exp_c": {2, 2}, "add_client_reset_exp_in": {2, 3}, "add_client_ip_limit_c": {2, 2}, "add_client_ip_limit_in": {2, 3},
	"get_inbounds": {1, 1}, "get_usage": {1, 1}, "usage_refresh": {1, 1}, "inbounds": {1, 1}, "deplete_soon": {1, 1}, "get_backup": {1, 1}, "get_banlogs": {1, 1}, "client_traffic": {1, 1}, "client_commands": {1, 1}, "onlines": {1, 1}, "onlines_refresh": {1, 1}, "commands": {1, 1},
	"add_client": {1, 1}, "add_client_ch_default_email": {1, 1}, "add_client_ch_default_id": {1, 1}, "add_client_ch_default_pass_tr": {1, 1}, "add_client_ch_default_pass_sh": {1, 1}, "add_client_ch_default_comment": {1, 1}, "add_client_ch_default_traffic": {1, 1}, "add_client_ch_default_exp": {1, 1}, "add_client_ch_default_ip_limit": {1, 1}, "add_client_default_info": {1, 1}, "add_client_cancel": {1, 1}, "add_client_default_traffic_exp": {1, 1}, "add_client_default_ip_limit": {1, 1}, "add_client_submit_disable": {1, 1}, "add_client_submit_enable": {1, 1},
	"reset_all_traffics_cancel": {1, 1}, "reset_all_traffics": {1, 1}, "reset_all_traffics_c": {1, 1}, "get_sorted_traffic_usage_report": {1, 1}, "subconverter_install": {1, 1}, "confirm_sub_install": {1, 1}, "cancel_sub_install": {1, 1}, "restart_panel": {1, 1}, "restart_panel_confirm": {1, 1}, "restart_panel_cancel": {1, 1},
}
