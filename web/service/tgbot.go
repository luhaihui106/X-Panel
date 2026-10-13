package service

import (
	"context"
	"crypto/rand"
	"crypto/tls" // 新增：用于 tls.Config
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net"
	"net/http" // 新增：用于 http.Client / Transport
	"net/url"
	"os"
	"os/exec"       // 新增：用于 exec.Command（getDomain 等）
	"path/filepath" // 新增：用于 filepath.Base / Dir（getDomain 用到）
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"x-ui/config"
	"x-ui/database"
	"x-ui/database/model"
	"x-ui/logger"
	"x-ui/util/common"
	"x-ui/web/global"
	"x-ui/web/locale"
	"x-ui/xray"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpproxy"
)

// 〔中文注释〕: 新增 TelegramService 接口，用于解耦 Job 和 Telegram Bot 的直接依赖。
// 任何实现了 SendMessage(msg string) error 方法的结构体，都可以被认为是 TelegramService。
type TelegramService interface {
	SendMessage(msg string) error
	SendSubconverterSuccess()
	IsRunning() bool
	// 您可以根据 server.go 的需要，在这里继续扩展接口
	// 〔中文注释〕: 将 SendOneClickConfig 方法添加到接口中，这样其他服务可以通过接口来调用它，
	// 实现了与具体实现 Tgbot 的解耦。
	// 新增 GetDomain 方法签名，以满足 server.go 的调用需求
	GetDomain() (string, error)
}

var (
	bot         *telego.Bot
	botHandler  *th.BotHandler
	adminIds    []int64
	isRunning   bool
	hostname    string
	hashStorage *global.HashStorage
)

type telegramClientDraft struct {
	receiver_inbound_ID int
	client_Id           string
	client_Flow         string
	client_Email        string
	client_LimitIP      int
	client_TotalGB      int64
	client_ExpiryTime   int64
	client_Enable       bool
	client_TgID         string
	client_SubID        string
	client_Comment      string
	client_Reset        int
	client_Security     string
	client_ShPassword   string
	client_TrPassword   string
	client_Method       string
}

var userStates = make(map[int64]string)

type LoginStatus byte

const (
	LoginSuccess        LoginStatus = 1
	LoginFail           LoginStatus = 0
	EmptyTelegramUserID             = int64(0)
)

type Tgbot struct {
	draft           *telegramClientDraft
	deferredDeletes sync.WaitGroup
	deleteOwner     *Tgbot
	effects         *telegramExternalEffects
	inboundService  *InboundService
	settingService  *SettingService
	serverService   *ServerService
	xrayService     *XrayService
	lastStatus      *Status
}

// 【新增方法】: 用于从外部注入 ServerService 实例
func (t *Tgbot) SetServerService(s *ServerService) {
	t.serverService = s
}

// 配合目前 main.go 代码结构实践。
func (t *Tgbot) SetInboundService(s *InboundService) {
	t.inboundService = s
}

// 〔中文注释〕: 在这里添加新的构造函数
// NewTgBot 创建并返回一个完全初始化的 Tgbot 实例。
// 这个函数确保了所有服务依赖项都被正确注入，避免了空指针问题。
func NewTgBot(
	inboundService *InboundService,
	settingService *SettingService,
	serverService *ServerService,
	xrayService *XrayService,
	lastStatus *Status,
) *Tgbot {
	return &Tgbot{
		inboundService: inboundService,
		settingService: settingService,
		serverService:  serverService,
		xrayService:    xrayService,
		lastStatus:     lastStatus,
	}
}

/*
func (t *Tgbot) NewTgbot() *Tgbot {
	return new(Tgbot)
}
*/

func (t *Tgbot) I18nBot(name string, params ...string) string {
	return locale.I18n(locale.Bot, name, params...)
}

func (t *Tgbot) GetHashStorage() *global.HashStorage {
	return hashStorage
}

func (t *Tgbot) Start(i18nFS embed.FS) error {
	// Initialize localizer
	err := locale.InitLocalizer(i18nFS, t.settingService)
	if err != nil {
		return err
	}

	// Initialize hash storage to store callback queries
	hashStorage = global.NewHashStorage(20 * time.Minute)

	t.SetHostname()

	// Get Telegram bot token
	tgBotToken, err := t.settingService.GetTgBotToken()
	if err != nil || tgBotToken == "" {
		logger.Warning("Failed to get Telegram bot token:", err)
		return err
	}

	// Get Telegram bot chat ID(s)
	tgBotID, err := t.settingService.GetTgBotChatId()
	if err != nil {
		logger.Warning("Failed to get Telegram bot chat ID:", err)
		return err
	}

	// Parse admin IDs from comma-separated string
	if tgBotID != "" {
		for _, adminID := range strings.Split(tgBotID, ",") {
			id, err := strconv.Atoi(adminID)
			if err != nil {
				logger.Warning("Failed to parse admin ID from Telegram bot chat ID:", err)
				return err
			}
			adminIds = append(adminIds, int64(id))
		}
	}

	// Get Telegram bot proxy URL
	tgBotProxy, err := t.settingService.GetTgBotProxy()
	if err != nil {
		logger.Warning("Failed to get Telegram bot proxy URL:", err)
	}

	// Get Telegram bot API server URL
	tgBotAPIServer, err := t.settingService.GetTgBotAPIServer()
	if err != nil {
		logger.Warning("Failed to get Telegram bot API server URL:", err)
	}

	// Create new Telegram bot instance
	bot, err = t.NewBot(tgBotToken, tgBotProxy, tgBotAPIServer)
	if err != nil {
		logger.Error("Failed to initialize Telegram bot API:", err)
		return err
	}

	// After bot initialization, set up bot commands with localized descriptions
	err = bot.SetMyCommands(context.Background(), &telego.SetMyCommandsParams{
		Commands: []telego.BotCommand{
			{Command: "start", Description: t.I18nBot("tgbot.commands.startDesc")},
			{Command: "help", Description: t.I18nBot("tgbot.commands.helpDesc")},
			{Command: "status", Description: t.I18nBot("tgbot.commands.statusDesc")},
			{Command: "id", Description: t.I18nBot("tgbot.commands.idDesc")},
			{Command: "subconverter", Description: "🔄 检测或安装订阅转换"},
			{Command: "restartx", Description: "♻️ 重启〔X-Panel 面板〕"},
		},
	})
	if err != nil {
		logger.Warning("Failed to set bot commands:", err)
	}

	// Start receiving Telegram bot messages
	if !isRunning {
		logger.Info("Telegram bot receiver started")
		go t.OnReceive()
		isRunning = true
	}

	return nil
}

func (t *Tgbot) NewBot(token string, proxyUrl string, apiServerUrl string) (*telego.Bot, error) {
	if proxyUrl == "" && apiServerUrl == "" {
		return telego.NewBot(token)
	}

	if proxyUrl != "" {
		if !strings.HasPrefix(proxyUrl, "socks5://") {
			logger.Warning("Invalid socks5 URL, using default")
			return telego.NewBot(token)
		}

		_, err := url.Parse(proxyUrl)
		if err != nil {
			logger.Warningf("Can't parse proxy URL, using default instance for tgbot: %v", err)
			return telego.NewBot(token)
		}

		return telego.NewBot(token, telego.WithFastHTTPClient(&fasthttp.Client{
			Dial: fasthttpproxy.FasthttpSocksDialer(proxyUrl),
		}))
	}

	if !strings.HasPrefix(apiServerUrl, "http") {
		logger.Warning("Invalid http(s) URL, using default")
		return telego.NewBot(token)
	}

	_, err := url.Parse(apiServerUrl)
	if err != nil {
		logger.Warningf("Can't parse API server URL, using default instance for tgbot: %v", err)
		return telego.NewBot(token)
	}

	return telego.NewBot(token, telego.WithAPIServer(apiServerUrl))
}

func (t *Tgbot) IsRunning() bool {
	return isRunning
}

func (t *Tgbot) SetHostname() {
	host, err := os.Hostname()
	if err != nil {
		logger.Error("get hostname error:", err)
		hostname = ""
		return
	}
	hostname = host
}

func (t *Tgbot) Stop() {
	if botHandler != nil {
		botHandler.Stop()
	}
	logger.Info("Stop Telegram receiver ...")
	isRunning = false
	adminIds = nil
}

func (t *Tgbot) encodeQuery(query string) string {
	// NOTE: we only need to hash for more than 64 chars
	if len(query) <= 64 {
		return query
	}

	return hashStorage.SaveHash(query)
}

func (t *Tgbot) decodeQuery(query string) (string, error) {
	if !hashStorage.IsMD5(query) {
		return query, nil
	}

	decoded, exists := hashStorage.GetValue(query)
	if !exists {
		return "", common.NewError("hash not found in storage!")
	}

	return decoded, nil
}

func (t *Tgbot) OnReceive() {
	params := telego.GetUpdatesParams{
		Timeout: 10,
	}

	updates, _ := bot.UpdatesViaLongPolling(context.Background(), &params)

	botHandler, _ = th.NewBotHandler(bot, updates)

	botHandler.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return t.handleCloseKeyboard(message)
	}, th.TextEqual(t.I18nBot("tgbot.buttons.closeKeyboard")))

	botHandler.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		t.answerCommand(&message, message.Chat.ID, checkAdmin(message.From.ID))
		return nil
	}, th.AnyCommand())

	botHandler.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		t.answerCallback(&query, checkAdmin(query.From.ID))
		return nil
	}, th.AnyCallbackQueryWithMessage())

	botHandler.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return t.handleTelegramMessage(message)
	}, th.AnyMessage())
	botHandler.Start()
}

func (t *Tgbot) handleCloseKeyboard(message telego.Message) error {
	telegramInteractionMu.Lock()
	defer telegramInteractionMu.Unlock()
	if message.From == nil || !privateTelegramActor(message.Chat, message.From.ID) {
		return nil
	}
	delete(userStates, message.Chat.ID)
	delete(wizardSessions, message.Chat.ID)
	delete(bindingRequests, message.Chat.ID)
	t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.keyboardClosed"), tu.ReplyKeyboardRemove())
	return nil
}

func (t *Tgbot) handleTelegramMessage(message telego.Message) error {
	telegramInteractionMu.Lock()
	defer telegramInteractionMu.Unlock()
	if message.From == nil || !privateTelegramActor(message.Chat, message.From.ID) || !checkAdmin(message.From.ID) {
		return nil
	}
	session := wizardSessions[message.From.ID]
	if message.UsersShared == nil && session != nil && session.inbound != nil {
		return t.handleInboundText(message)
	}
	if message.UsersShared == nil && (session == nil || !time.Now().Before(session.until)) {
		return nil
	}
	if session != nil {
		t = t.withDraft(session.draft)
	}
	if message.UsersShared != nil {
		pending, ok := bindingRequests[message.From.ID]
		if !ok || int(pending.id) != message.UsersShared.RequestID || !time.Now().Before(pending.until) {
			return nil
		}
		message.UsersShared.RequestID = pending.client
		delete(bindingRequests, message.From.ID)
	}
	if userState, exists := userStates[message.Chat.ID]; exists && message.UsersShared == nil {
		switch userState {
		case "awaiting_id":
			if t.draft.client_Id == strings.TrimSpace(message.Text) {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				t.addClient(message.Chat.ID, message_text)
				return nil
			}

			t.draft.client_Id = strings.TrimSpace(message.Text)
			if t.isSingleWord(t.draft.client_Id) {
				userStates[message.Chat.ID] = "awaiting_id"

				cancel_btn_markup := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
					),
				)

				t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
			} else {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_id"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				t.addClient(message.Chat.ID, message_text)
			}
		case "awaiting_password_tr":
			if t.draft.client_TrPassword == strings.TrimSpace(message.Text) {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				return nil
			}

			t.draft.client_TrPassword = strings.TrimSpace(message.Text)
			if t.isSingleWord(t.draft.client_TrPassword) {
				userStates[message.Chat.ID] = "awaiting_password_tr"

				cancel_btn_markup := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
					),
				)

				t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
			} else {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_password"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				t.addClient(message.Chat.ID, message_text)
			}
		case "awaiting_password_sh":
			if t.draft.client_ShPassword == strings.TrimSpace(message.Text) {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				return nil
			}

			t.draft.client_ShPassword = strings.TrimSpace(message.Text)
			if t.isSingleWord(t.draft.client_ShPassword) {
				userStates[message.Chat.ID] = "awaiting_password_sh"

				cancel_btn_markup := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
					),
				)

				t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
			} else {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_password"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				t.addClient(message.Chat.ID, message_text)
			}
		case "awaiting_email":
			if t.draft.client_Email == strings.TrimSpace(message.Text) {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				return nil
			}

			t.draft.client_Email = strings.TrimSpace(message.Text)
			if t.isSingleWord(t.draft.client_Email) {
				userStates[message.Chat.ID] = "awaiting_email"

				cancel_btn_markup := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
					),
				)

				t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.messages.incorrect_input"), cancel_btn_markup)
			} else {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_email"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				t.addClient(message.Chat.ID, message_text)
			}
		case "awaiting_comment":
			if t.draft.client_Comment == strings.TrimSpace(message.Text) {
				t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
				delete(userStates, message.Chat.ID)
				return nil
			}

			t.draft.client_Comment = strings.TrimSpace(message.Text)
			t.SendMsgToTgbotDeleteAfter(message.Chat.ID, t.I18nBot("tgbot.messages.received_comment"), 3, tu.ReplyKeyboardRemove())
			delete(userStates, message.Chat.ID)
			inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
			message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
			t.addClient(message.Chat.ID, message_text)
		}

	} else {
		if message.UsersShared != nil {
			if checkAdmin(message.From.ID) {
				for _, sharedUser := range message.UsersShared.Users {
					userID := sharedUser.UserID
					needRestart, err := t.inboundService.SetClientTelegramUserID(message.UsersShared.RequestID, userID)
					if needRestart {
						t.xrayService.SetToNeedRestart()
					}
					output := ""
					if err != nil {
						output += t.I18nBot("tgbot.messages.selectUserFailed")
					} else {
						output += t.I18nBot("tgbot.messages.userSaved")
					}
					t.SendMsgToTgbot(message.Chat.ID, output, tu.ReplyKeyboardRemove())
				}
			} else {
				t.SendMsgToTgbot(message.Chat.ID, t.I18nBot("tgbot.noResult"), tu.ReplyKeyboardRemove())
			}
		}
	}
	return nil
}

func (t *Tgbot) answerCommand(message *telego.Message, chatId int64, isAdmin bool) {
	telegramInteractionMu.Lock()
	defer telegramInteractionMu.Unlock()
	if message == nil || message.From == nil || !privateTelegramActor(message.Chat, message.From.ID) || chatId != message.From.ID {
		return
	}
	isAdmin = checkAdmin(message.From.ID)
	delete(userStates, message.From.ID)
	msg, onlyMessage := "", false

	command, _, commandArgs := tu.ParseCommand(message.Text)

	// Helper function to handle unknown commands.
	handleUnknownCommand := func() {
		msg += t.I18nBot("tgbot.commands.unknown")
	}

	// Handle the command.
	switch command {
	case "help":
		msg += t.I18nBot("tgbot.commands.help")
		msg += t.I18nBot("tgbot.commands.pleaseChoose")
	case "start":
		msg += t.I18nBot("tgbot.commands.start", "Firstname=="+message.From.FirstName)
		if isAdmin {
			msg += t.I18nBot("tgbot.commands.welcome", "Hostname=="+hostname)
		}
		msg += "\n\n" + t.I18nBot("tgbot.commands.pleaseChoose")
	case "status":
		onlyMessage = true
		msg += t.I18nBot("tgbot.commands.status")
	case "id":
		onlyMessage = true
		msg += t.I18nBot("tgbot.commands.getID", "ID=="+strconv.FormatInt(message.From.ID, 10))
	case "usage":
		onlyMessage = true
		if len(commandArgs) > 0 {
			if isAdmin {
				t.searchClient(chatId, commandArgs[0])
			} else {
				t.getClientUsage(chatId, int64(message.From.ID), commandArgs[0])
			}
		} else {
			msg += t.I18nBot("tgbot.commands.usage")
		}
	case "inbound":
		onlyMessage = true
		if isAdmin && len(commandArgs) > 0 {
			t.searchInbound(chatId, commandArgs[0])
		} else {
			handleUnknownCommand()
		}
	case "restart":
		onlyMessage = true
		if isAdmin {
			if len(commandArgs) == 0 {
				t.SendMsgToTgbot(chatId, "预览：重启 Xray Core，现有连接可能短暂中断。确认后执行。", tu.InlineKeyboard(tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("确认重启").WithCallbackData("restart_core_confirm"),
					tu.InlineKeyboardButton("取消").WithCallbackData("restart_core_cancel"))))
			} else {
				handleUnknownCommand()
				msg += t.I18nBot("tgbot.commands.restartUsage")
			}
		} else {
			handleUnknownCommand()
		}
	// 【新增代码】: 处理 /subconverter 指令
	case "subconverter":
		onlyMessage = true
		if isAdmin {
			t.checkAndInstallSubconverter(chatId)
		} else {
			handleUnknownCommand()
		}

	// 〔中文注释〕: 【新增代码】: 处理 /restartx 指令，用于重启面板
	case "restartx":
		onlyMessage = true
		if isAdmin {
			// 〔中文注释〕: 发送重启确认消息
			confirmKeyboard := tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("✅ 是，立即重启").WithCallbackData(t.encodeQuery("restart_panel_confirm")),
				),
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("❌ 否，我再想想").WithCallbackData(t.encodeQuery("restart_panel_cancel")),
				),
			)
			// 〔中文注释〕: 从您提供的需求中引用提示文本
			t.SendMsgToTgbot(chatId, "🤔 您“现在的操作”是要确定进行，\n\n重启〔X-Panel 面板〕服务吗？\n\n这也会同时重启 Xray Core，\n\n会使面板在短时间内无法访问。", confirmKeyboard)
		} else {
			handleUnknownCommand()
		}
	default:
		handleUnknownCommand()
	}

	if msg != "" {
		t.sendResponse(chatId, msg, onlyMessage, isAdmin)
	}
}

// Helper function to send the message based on onlyMessage flag.
func (t *Tgbot) sendResponse(chatId int64, msg string, onlyMessage, isAdmin bool) {
	if onlyMessage {
		t.SendMsgToTgbot(chatId, msg)
	} else {
		t.SendAnswer(chatId, msg, isAdmin)
	}
}

func (t *Tgbot) randomLowerAndNum(length int) string {
	charset := "abcdefghijklmnopqrstuvwxyz0123456789"
	bytes := make([]byte, length)
	for i := range bytes {
		randomIndex, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		bytes[i] = charset[randomIndex.Int64()]
	}
	return string(bytes)
}

func (t *Tgbot) randomShadowSocksPassword() string {
	array := make([]byte, 32)
	_, err := rand.Read(array)
	if err != nil {
		return t.randomLowerAndNum(32)
	}
	return base64.StdEncoding.EncodeToString(array)
}

func (t *Tgbot) answerCallback(callbackQuery *telego.CallbackQuery, isAdmin bool) {
	telegramInteractionMu.Lock()
	defer telegramInteractionMu.Unlock()
	decoded, allowed := t.authorizeCallback(callbackQuery)
	if !allowed {
		t.denyCallback(callbackQuery)
		return
	}
	isAdmin = checkAdmin(callbackQuery.From.ID)
	delete(userStates, callbackQuery.From.ID)
	chatId := callbackQuery.Message.GetChat().ID
	copyQuery := *callbackQuery
	copyQuery.Data = decoded
	callbackQuery = &copyQuery
	if strings.HasPrefix(decoded, "inbound_result_") {
		t.handleInboundResult(callbackQuery, decoded)
		return
	}
	if strings.HasPrefix(decoded, "add_inbound") {
		t.handleInboundCallback(callbackQuery, decoded)
		return
	}
	if session := wizardSessions[chatId]; session != nil {
		t = t.withDraft(session.draft)
	}
	if !isAdmin && strings.HasPrefix(decoded, "client_get_usage ") {
		t.getClientUsage(chatId, callbackQuery.From.ID, strings.TrimPrefix(decoded, "client_get_usage "))
		return
	}

	if isAdmin {
		// get query from hash storage
		decodedQuery, err := t.decodeQuery(callbackQuery.Data)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.noQuery"))
			return
		}
		dataArray := strings.Split(decodedQuery, " ")

		if len(dataArray) >= 2 && len(dataArray[1]) > 0 {
			email := dataArray[1]
			switch dataArray[0] {
			case "client_get_usage":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.messages.email", "Email=="+email))
				t.searchClient(chatId, email)
			case "client_refresh":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.clientRefreshSuccess", "Email=="+email))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "client_cancel":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+email))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "ips_refresh":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.IpRefreshSuccess", "Email=="+email))
				t.searchClientIps(chatId, email, callbackQuery.Message.GetMessageID())
			case "ips_cancel":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+email))
				t.searchClientIps(chatId, email, callbackQuery.Message.GetMessageID())
			case "tgid_refresh":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.TGIdRefreshSuccess", "Email=="+email))
				t.clientTelegramUserInfo(chatId, email, callbackQuery.Message.GetMessageID())
			case "tgid_cancel":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+email))
				t.clientTelegramUserInfo(chatId, email, callbackQuery.Message.GetMessageID())
			case "reset_traffic":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmResetTraffic")).WithCallbackData(t.encodeQuery("reset_traffic_c "+email)),
					),
				)
				t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), "预览：清零客户端 "+email+" 的当前流量；确认后执行。", inlineKeyboard)
			case "reset_traffic_c":
				err := t.inboundService.ResetClientTrafficByEmail(email)
				if err == nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.resetTrafficSuccess", "Email=="+email))
					t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "limit_traffic":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 0")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" 0")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("1 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 1")),
						tu.InlineKeyboardButton("5 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 5")),
						tu.InlineKeyboardButton("10 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 10")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("20 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 20")),
						tu.InlineKeyboardButton("30 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 30")),
						tu.InlineKeyboardButton("40 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 40")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("50 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 50")),
						tu.InlineKeyboardButton("60 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 60")),
						tu.InlineKeyboardButton("80 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 80")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("100 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 100")),
						tu.InlineKeyboardButton("150 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 150")),
						tu.InlineKeyboardButton("200 GB").WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" 200")),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "limit_traffic_c":
				if len(dataArray) == 3 {
					limitTraffic, err := strconv.Atoi(dataArray[2])
					if err == nil {
						needRestart, err := t.inboundService.ResetClientTrafficLimitByEmail(email, limitTraffic)
						if needRestart {
							t.xrayService.SetToNeedRestart()
						}
						if err == nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.setTrafficLimitSuccess", "Email=="+email))
							t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
							return
						}
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "limit_traffic_in":
				if len(dataArray) >= 3 {
					oldInputNumber, err := strconv.Atoi(dataArray[2])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 4 {
							num, err := strconv.Atoi(dataArray[3])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("limit_traffic_c "+email+" "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("limit_traffic_in "+email+" "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "add_client_limit_traffic_c":
				limitTraffic, _ := strconv.Atoi(dataArray[1])
				t.draft.client_TotalGB = int64(limitTraffic) * 1024 * 1024 * 1024
				messageId := callbackQuery.Message.GetMessageID()
				inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text, messageId)
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
			case "add_client_limit_traffic_in":
				if len(dataArray) >= 2 {
					oldInputNumber, err := strconv.Atoi(dataArray[1])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 3 {
							num, err := strconv.Atoi(dataArray[2])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("add_client_limit_traffic_c "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("add_client_limit_traffic_in "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
			case "reset_exp":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 0")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("reset_exp_in "+email+" 0")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 7 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 7")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 10 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 10")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 14 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 14")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 20 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 20")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 1 "+t.I18nBot("tgbot.month")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 30")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 3 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 90")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 6 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 180")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 12 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" 365")),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "reset_exp_c":
				if len(dataArray) == 3 {
					days, err := strconv.Atoi(dataArray[2])
					if err == nil {
						var date int64 = 0
						if days > 0 {
							traffic, err := t.inboundService.GetClientTrafficByEmail(email)
							if err != nil {
								logger.Warning(err)
								msg := t.I18nBot("tgbot.wentWrong")
								t.SendMsgToTgbot(chatId, msg)
								return
							}
							if traffic == nil {
								msg := t.I18nBot("tgbot.noResult")
								t.SendMsgToTgbot(chatId, msg)
								return
							}

							if traffic.ExpiryTime > 0 {
								if traffic.ExpiryTime-time.Now().Unix()*1000 < 0 {
									date = -int64(days * 24 * 60 * 60000)
								} else {
									date = traffic.ExpiryTime + int64(days*24*60*60000)
								}
							} else {
								date = traffic.ExpiryTime - int64(days*24*60*60000)
							}

						}
						needRestart, err := t.inboundService.ResetClientExpiryTimeByEmail(email, date)
						if needRestart {
							t.xrayService.SetToNeedRestart()
						}
						if err == nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.expireResetSuccess", "Email=="+email))
							t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
							return
						}
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "reset_exp_in":
				if len(dataArray) >= 3 {
					oldInputNumber, err := strconv.Atoi(dataArray[2])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 4 {
							num, err := strconv.Atoi(dataArray[3])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumber", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("reset_exp_c "+email+" "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("reset_exp_in "+email+" "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "add_client_reset_exp_c":
				t.draft.client_ExpiryTime = 0
				days, _ := strconv.Atoi(dataArray[1])
				var date int64 = 0
				if t.draft.client_ExpiryTime > 0 {
					if t.draft.client_ExpiryTime-time.Now().Unix()*1000 < 0 {
						date = -int64(days * 24 * 60 * 60000)
					} else {
						date = t.draft.client_ExpiryTime + int64(days*24*60*60000)
					}
				} else {
					date = t.draft.client_ExpiryTime - int64(days*24*60*60000)
				}
				t.draft.client_ExpiryTime = date

				messageId := callbackQuery.Message.GetMessageID()
				inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text, messageId)
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
			case "add_client_reset_exp_in":
				if len(dataArray) >= 2 {
					oldInputNumber, err := strconv.Atoi(dataArray[1])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 3 {
							num, err := strconv.Atoi(dataArray[2])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumberAdd", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("add_client_reset_exp_c "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("add_client_reset_exp_in "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
			case "ip_limit":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelIpLimit")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 0")),
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("ip_limit_in "+email+" 0")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 1")),
						tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 2")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 3")),
						tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 4")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 5")),
						tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 6")),
						tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 7")),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 8")),
						tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 9")),
						tu.InlineKeyboardButton("10").WithCallbackData(t.encodeQuery("ip_limit_c "+email+" 10")),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "ip_limit_c":
				if len(dataArray) == 3 {
					count, err := strconv.Atoi(dataArray[2])
					if err == nil {
						needRestart, err := t.inboundService.ResetClientIpLimitByEmail(email, count)
						if needRestart {
							t.xrayService.SetToNeedRestart()
						}
						if err == nil {
							t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.resetIpSuccess", "Email=="+email, "Count=="+strconv.Itoa(count)))
							t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
							return
						}
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "ip_limit_in":
				if len(dataArray) >= 3 {
					oldInputNumber, err := strconv.Atoi(dataArray[2])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 4 {
							num, err := strconv.Atoi(dataArray[3])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumber", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("ip_limit_c "+email+" "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("ip_limit_in "+email+" "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
			case "add_client_ip_limit_c":
				if len(dataArray) == 2 {
					count, _ := strconv.Atoi(dataArray[1])
					t.draft.client_LimitIP = count
				}

				messageId := callbackQuery.Message.GetMessageID()
				inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text, messageId)
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
			case "add_client_ip_limit_in":
				if len(dataArray) >= 2 {
					oldInputNumber, err := strconv.Atoi(dataArray[1])
					inputNumber := oldInputNumber
					if err == nil {
						if len(dataArray) == 3 {
							num, err := strconv.Atoi(dataArray[2])
							if err == nil {
								switch num {
								case -2:
									inputNumber = 0
								case -1:
									if inputNumber > 0 {
										inputNumber = (inputNumber / 10)
									}
								default:
									inputNumber = (inputNumber * 10) + num
								}
							}
							if inputNumber == oldInputNumber {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
								return
							}
							if inputNumber >= 999999 {
								t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
								return
							}
						}
						inlineKeyboard := tu.InlineKeyboard(
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_ip_limit")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmNumber", "Num=="+strconv.Itoa(inputNumber))).WithCallbackData(t.encodeQuery("add_client_ip_limit_c "+strconv.Itoa(inputNumber))),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 1")),
								tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 2")),
								tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 3")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 4")),
								tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 5")),
								tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 6")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 7")),
								tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 8")),
								tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 9")),
							),
							tu.InlineKeyboardRow(
								tu.InlineKeyboardButton("🔄").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" -2")),
								tu.InlineKeyboardButton("0").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" 0")),
								tu.InlineKeyboardButton("⬅️").WithCallbackData(t.encodeQuery("add_client_ip_limit_in "+strconv.Itoa(inputNumber)+" -1")),
							),
						)
						t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
						return
					}
				}
			case "clear_ips":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("ips_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmClearIps")).WithCallbackData(t.encodeQuery("clear_ips_c "+email)),
					),
				)
				t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), "预览：删除客户端 "+email+" 的 IP 记录；确认后执行。", inlineKeyboard)
			case "clear_ips_c":
				err := t.inboundService.ClearClientIps(email)
				if err == nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.clearIpSuccess", "Email=="+email))
					t.searchClientIps(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "ip_log":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.getIpLog", "Email=="+email))
				t.searchClientIps(chatId, email)
			case "tg_user":
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.getUserInfo", "Email=="+email))
				t.clientTelegramUserInfo(chatId, email)
			case "tgid_remove":
				delete(bindingRequests, chatId)
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("tgid_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmRemoveTGUser")).WithCallbackData(t.encodeQuery("tgid_remove_c "+email)),
					),
				)
				t.editMessageTgBot(chatId, callbackQuery.Message.GetMessageID(), "预览：删除客户端 "+email+" 的 Telegram 用户绑定；不会删除客户端。确认后执行。", inlineKeyboard)
			case "tgid_remove_c":
				if traffic, err := t.inboundService.GetClientTrafficByEmail(email); err == nil && traffic != nil {
					for actor, pending := range bindingRequests {
						if pending.client == traffic.Id {
							delete(bindingRequests, actor)
						}
					}
				}
				traffic, err := t.inboundService.GetClientTrafficByEmail(email)
				if err != nil || traffic == nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
					return
				}
				needRestart, err := t.inboundService.SetClientTelegramUserID(traffic.Id, EmptyTelegramUserID)
				if needRestart {
					t.xrayService.SetToNeedRestart()
				}
				if err == nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.removedTGUserSuccess", "Email=="+email))
					t.clientTelegramUserInfo(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "toggle_enable":
				inlineKeyboard := tu.InlineKeyboard(
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("client_cancel "+email)),
					),
					tu.InlineKeyboardRow(
						tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmToggle")).WithCallbackData(t.encodeQuery("toggle_enable_c "+email)),
					),
				)
				t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
			case "toggle_enable_c":
				enabled, needRestart, err := t.inboundService.ToggleClientEnableByEmail(email)
				if needRestart {
					t.xrayService.SetToNeedRestart()
				}
				if err == nil {
					if enabled {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.enableSuccess", "Email=="+email))
					} else {
						t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.disableSuccess", "Email=="+email))
					}
					t.searchClient(chatId, email, callbackQuery.Message.GetMessageID())
				} else {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.errorOperation"))
				}
			case "get_clients":
				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				clients, err := t.getInboundClients(inboundIdInt)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseClient", "Inbound=="+inbound.Remark), clients)
			case "add_client_to":
				t.initializeClientDraft()

				inboundId := dataArray[1]
				inboundIdInt, err := strconv.Atoi(inboundId)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}
				t.draft.receiver_inbound_ID = inboundIdInt
				inbound, err := t.inboundService.GetInbound(inboundIdInt)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return
				}

				t.addClient(callbackQuery.Message.GetChat().ID, message_text)
			}
			return
		} else {
			switch callbackQuery.Data {
			case "get_inbounds":
				inbounds, err := t.getInbounds()
				if err != nil {
					t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
					return

				}
				t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.allClients"))
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
			}

		}
	}

	switch callbackQuery.Data {
	case "restart_core_confirm":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		if t.effects != nil && t.effects.restartCore != nil {
			t.effects.restartCore(chatId)
		} else if t.xrayService.IsXrayRunning() {
			if err := t.xrayService.RestartXray(true); err != nil {
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.restartFailed", "Error=="+err.Error()))
			} else {
				t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.restartSuccess"))
			}
		} else {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.xrayNotRunning"))
		}
	case "restart_core_cancel":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.SendMsgToTgbot(chatId, "已取消重启 Xray Core。")
	case "menu_root", "menu_status", "menu_clients", "menu_backup", "menu_system":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendAdminGroup(chatId, callbackQuery.Data)
	case "get_usage":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.serverUsage"))
		t.getServerUsage(chatId)
	case "usage_refresh":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
		t.getServerUsage(chatId, callbackQuery.Message.GetMessageID())
	case "inbounds":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.getInbounds"))
		t.SendMsgToTgbot(chatId, t.getInboundUsages())
	case "deplete_soon":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.depleteSoon"))
		t.getExhausted(chatId)
	case "get_backup":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.dbBackup"))
		if t.effects != nil {
			t.effects.backup(chatId)
		} else {
			t.sendBackup(chatId)
		}
	case "get_banlogs":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.getBanLogs"))
		t.sendBanLogs(chatId, true)
	case "client_traffic":
		tgUserID := callbackQuery.From.ID
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.clientUsage"))
		t.getClientUsage(chatId, tgUserID)
	case "client_commands":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.commands"))
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.helpClientCommands"))
	case "onlines":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.onlines"))
		t.onlineClients(chatId)
	case "onlines_refresh":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.successfulOperation"))
		t.onlineClients(chatId, callbackQuery.Message.GetMessageID())
	case "commands":
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.commands"))
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.commands.helpAdminCommands"))
	case "add_client":
		t.initializeClientDraft()

		inbounds, err := t.getInboundsAddClient()
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.buttons.addClient"))
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.chooseInbound"), inbounds)
	case "add_client_ch_default_email":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_email"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.email_prompt", "ClientEmail=="+t.draft.client_Email)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_id":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_id"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.id_prompt", "ClientId=="+t.draft.client_Id)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_pass_tr":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_password_tr"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.pass_prompt", "ClientPassword=="+t.draft.client_TrPassword)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_pass_sh":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_password_sh"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.pass_prompt", "ClientPassword=="+t.draft.client_ShPassword)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_comment":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		userStates[chatId] = "awaiting_comment"
		cancel_btn_markup := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.use_default")).WithCallbackData("add_client_default_info"),
			),
		)
		prompt_message := t.I18nBot("tgbot.messages.comment_prompt", "ClientComment=="+t.draft.client_Comment)
		t.SendMsgToTgbot(chatId, prompt_message, cancel_btn_markup)
	case "add_client_ch_default_traffic":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 0")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("add_client_limit_traffic_in 0")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("1 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 1")),
				tu.InlineKeyboardButton("5 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 5")),
				tu.InlineKeyboardButton("10 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 10")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("20 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 20")),
				tu.InlineKeyboardButton("30 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 30")),
				tu.InlineKeyboardButton("40 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 40")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("50 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 50")),
				tu.InlineKeyboardButton("60 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 60")),
				tu.InlineKeyboardButton("80 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 80")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("100 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 100")),
				tu.InlineKeyboardButton("150 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 150")),
				tu.InlineKeyboardButton("200 GB").WithCallbackData(t.encodeQuery("add_client_limit_traffic_c 200")),
			),
		)
		t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
	case "add_client_ch_default_exp":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_traffic_exp")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 0")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("add_client_reset_exp_in 0")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 7 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 7")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 10 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 10")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 14 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 14")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 20 "+t.I18nBot("tgbot.days")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 20")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 1 "+t.I18nBot("tgbot.month")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 30")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 3 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 90")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 6 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 180")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.add")+" 12 "+t.I18nBot("tgbot.months")).WithCallbackData(t.encodeQuery("add_client_reset_exp_c 365")),
			),
		)
		t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
	case "add_client_ch_default_ip_limit":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData(t.encodeQuery("add_client_default_ip_limit")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.unlimited")).WithCallbackData(t.encodeQuery("add_client_ip_limit_c 0")),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.custom")).WithCallbackData(t.encodeQuery("add_client_ip_limit_in 0")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("1").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 1")),
				tu.InlineKeyboardButton("2").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 2")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("3").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 3")),
				tu.InlineKeyboardButton("4").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 4")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("5").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 5")),
				tu.InlineKeyboardButton("6").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 6")),
				tu.InlineKeyboardButton("7").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 7")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("8").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 8")),
				tu.InlineKeyboardButton("9").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 9")),
				tu.InlineKeyboardButton("10").WithCallbackData(t.encodeQuery("add_client_ip_limit_c 10")),
			),
		)
		t.editMessageCallbackTgBot(chatId, callbackQuery.Message.GetMessageID(), inlineKeyboard)
	case "add_client_default_info":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.messages.using_default_value"), 3, tu.ReplyKeyboardRemove())
		delete(userStates, chatId)
		inbound, _ := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
		message_text, _ := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		t.addClient(chatId, message_text)
	case "add_client_cancel":
		delete(wizardSessions, chatId)
		delete(userStates, chatId)
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.messages.cancel"), 3, tu.ReplyKeyboardRemove())
	case "add_client_default_traffic_exp":
		messageId := callbackQuery.Message.GetMessageID()
		inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.addClient(chatId, message_text, messageId)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+t.draft.client_Email))
	case "add_client_default_ip_limit":
		messageId := callbackQuery.Message.GetMessageID()
		inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		message_text, err := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol)
		if err != nil {
			t.sendCallbackAnswerTgBot(callbackQuery.ID, err.Error())
			return
		}
		t.addClient(chatId, message_text, messageId)
		t.sendCallbackAnswerTgBot(callbackQuery.ID, t.I18nBot("tgbot.answers.canceled", "Email=="+t.draft.client_Email))
	case "add_client_submit_disable":
		t.draft.client_Enable = false
		_, err := t.SubmitAddClient()
		if err != nil {
			errorMessage := fmt.Sprintf("%v", err)
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.error_add_client", "error=="+errorMessage), tu.ReplyKeyboardRemove())
			if inbound, e := t.inboundService.GetInbound(t.draft.receiver_inbound_ID); e == nil {
				if text, e := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol); e == nil {
					t.addClient(chatId, text)
				}
			}
		} else {
			delete(wizardSessions, chatId)
			t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.successfulOperation"), tu.ReplyKeyboardRemove())
		}
	case "add_client_submit_enable":
		t.draft.client_Enable = true
		_, err := t.SubmitAddClient()
		if err != nil {
			errorMessage := fmt.Sprintf("%v", err)
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.error_add_client", "error=="+errorMessage), tu.ReplyKeyboardRemove())
			if inbound, e := t.inboundService.GetInbound(t.draft.receiver_inbound_ID); e == nil {
				if text, e := t.BuildInboundClientDataMessage(inbound.Remark, inbound.Protocol); e == nil {
					t.addClient(chatId, text)
				}
			}
		} else {
			delete(wizardSessions, chatId)
			t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.successfulOperation"), tu.ReplyKeyboardRemove())
		}
	case "reset_all_traffics_cancel":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.SendMsgToTgbotDeleteAfter(chatId, t.I18nBot("tgbot.messages.cancel"), 1, tu.ReplyKeyboardRemove())
	case "reset_all_traffics":
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancelReset")).WithCallbackData(t.encodeQuery("reset_all_traffics_cancel")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.confirmResetTraffic")).WithCallbackData(t.encodeQuery("reset_all_traffics_c")),
			),
		)
		t.SendMsgToTgbot(chatId, "预览：清零全部客户端的当前流量；此操作不能撤销。确认后执行。", inlineKeyboard)
	case "reset_all_traffics_c":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		emails, err := t.inboundService.getAllEmails()
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation"), tu.ReplyKeyboardRemove())
			return
		}

		for _, email := range emails {
			err := t.inboundService.ResetClientTrafficByEmail(email)
			if err == nil {
				msg := t.I18nBot("tgbot.messages.SuccessResetTraffic", "ClientEmail=="+email)
				t.SendMsgToTgbot(chatId, msg, tu.ReplyKeyboardRemove())
			} else {
				msg := t.I18nBot("tgbot.messages.FailedResetTraffic", "ClientEmail=="+email, "ErrorMessage=="+err.Error())
				t.SendMsgToTgbot(chatId, msg, tu.ReplyKeyboardRemove())
			}
		}

		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.messages.FinishProcess"), tu.ReplyKeyboardRemove())
	case "get_sorted_traffic_usage_report":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		emails, err := t.inboundService.getAllEmails()

		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation"), tu.ReplyKeyboardRemove())
			return
		}
		valid_emails, extra_emails, err := t.inboundService.FilterAndSortClientEmails(emails)
		if err != nil {
			t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.errorOperation"), tu.ReplyKeyboardRemove())
			return
		}

		for _, valid_emails := range valid_emails {
			traffic, err := t.inboundService.GetClientTrafficByEmail(valid_emails)
			if err != nil {
				logger.Warning(err)
				msg := t.I18nBot("tgbot.wentWrong")
				t.SendMsgToTgbot(chatId, msg)
				continue
			}
			if traffic == nil {
				msg := t.I18nBot("tgbot.noResult")
				t.SendMsgToTgbot(chatId, msg)
				continue
			}

			output := t.clientInfoMsg(traffic, false, false, false, false, true, false)
			t.SendMsgToTgbot(chatId, output, tu.ReplyKeyboardRemove())
		}
		for _, extra_emails := range extra_emails {
			msg := fmt.Sprintf("📧 %s\n%s", extra_emails, t.I18nBot("tgbot.noResult"))
			t.SendMsgToTgbot(chatId, msg, tu.ReplyKeyboardRemove())

		}

	case "subconverter_install":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "🔄 正在检查服务...")
		if t.effects != nil {
			t.effects.checkInstall(chatId)
		} else {
			t.checkAndInstallSubconverter(chatId)
		}

	case "confirm_sub_install":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "✅ 指令已发送")
		t.SendMsgToTgbot(chatId, "【订阅转换】模块正在后台安装，大约需要1-2分钟，完成后将再次通知您。")
		var err error
		if t.effects != nil {
			err = t.effects.install()
		} else {
			err = t.serverService.InstallSubconverter()
		}
		if err != nil {
			t.SendMsgToTgbot(chatId, fmt.Sprintf("发送安装指令失败: %v", err))
		}

	case "cancel_sub_install":
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "已取消")
		t.SendMsgToTgbot(chatId, "已取消【订阅转换】安装操作。")
	// 〔中文注释〕: 【新增回调处理】 - 重启面板、娱乐抽奖、VPS推荐
	case "restart_panel":
		// 〔中文注释〕: 用户从菜单点击重启，删除主菜单并发送确认消息
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "请确认操作")
		confirmKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("✅ 是，立即重启").WithCallbackData(t.encodeQuery("restart_panel_confirm")),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("❌ 否，我再想想").WithCallbackData(t.encodeQuery("restart_panel_cancel")),
			),
		)
		t.SendMsgToTgbot(chatId, "🤔 您“现在的操作”是要确定进行，\n\n重启〔X-Panel 面板〕服务吗？\n\n这也会同时重启 Xray Core，\n\n会使面板在短时间内无法访问。", confirmKeyboard)

	case "restart_panel_confirm":
		// 〔中文注释〕: 用户确认重启
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "指令已发送，请稍候...")
		t.SendMsgToTgbot(chatId, "⏳ 【重启命令】已在 VPS 中远程执行，\n\n正在等待面板恢复（约30秒），并进行验证检查...")

		// 〔中文注释〕: 在后台协程中执行重启，避免阻塞机器人
		if t.effects != nil {
			t.effects.restart(chatId)
			return
		}
		go func() {
			err := t.serverService.RestartPanel()
			// 〔中文注释〕: 等待20秒，让面板有足够的时间重启
			time.Sleep(20 * time.Second)
			if err != nil {
				// 〔中文注释〕: 如果执行出错，发送失败消息
				t.SendMsgToTgbot(chatId, fmt.Sprintf("❌ 面板重启命令执行失败！\n\n错误信息已记录到日志，请检查命令或权限。\n\n`%v`", err))
			} else {
				// 〔中文注释〕: 执行成功，发送成功消息
				t.SendMsgToTgbot(chatId, "🚀 面板重启成功！服务已成功恢复！")
			}
		}()

	case "restart_panel_cancel":
		// 〔中文注释〕: 用户取消重启
		t.deleteMessageTgBot(chatId, callbackQuery.Message.GetMessageID())
		t.sendCallbackAnswerTgBot(callbackQuery.ID, "操作已取消")
		// 〔中文注释〕: 发送一个临时消息提示用户，3秒后自动删除
		t.SendMsgToTgbotDeleteAfter(chatId, "已取消重启操作。", 3)

	}
}

func (t *Tgbot) BuildInboundClientDataMessage(inbound_remark string, protocol model.Protocol) (string, error) {
	var message string

	currentTime := time.Now()
	timestampMillis := currentTime.UnixNano() / int64(time.Millisecond)

	expiryTime := ""
	diff := t.draft.client_ExpiryTime/1000 - timestampMillis
	if t.draft.client_ExpiryTime == 0 {
		expiryTime = t.I18nBot("tgbot.unlimited")
	} else if diff > 172800 {
		expiryTime = time.Unix((t.draft.client_ExpiryTime / 1000), 0).Format("2006-01-02 15:04:05")
	} else if t.draft.client_ExpiryTime < 0 {
		expiryTime = fmt.Sprintf("%d %s", t.draft.client_ExpiryTime/-86400000, t.I18nBot("tgbot.days"))
	} else {
		expiryTime = fmt.Sprintf("%d %s", diff/3600, t.I18nBot("tgbot.hours"))
	}

	traffic_value := ""
	if t.draft.client_TotalGB == 0 {
		traffic_value = "♾️ Unlimited(Reset)"
	} else {
		traffic_value = common.FormatTraffic(t.draft.client_TotalGB)
	}

	ip_limit := ""
	if t.draft.client_LimitIP == 0 {
		ip_limit = "♾️ Unlimited(Reset)"
	} else {
		ip_limit = fmt.Sprint(t.draft.client_LimitIP)
	}

	switch protocol {
	case model.VMESS, model.VLESS:
		message = t.I18nBot("tgbot.messages.inbound_client_data_id", "InboundRemark=="+inbound_remark, "ClientId=="+t.draft.client_Id, "ClientEmail=="+t.draft.client_Email, "ClientTraffic=="+traffic_value, "ClientExp=="+expiryTime, "IpLimit=="+ip_limit, "ClientComment=="+t.draft.client_Comment)

	case model.Trojan:
		message = t.I18nBot("tgbot.messages.inbound_client_data_pass", "InboundRemark=="+inbound_remark, "ClientPass=="+t.draft.client_TrPassword, "ClientEmail=="+t.draft.client_Email, "ClientTraffic=="+traffic_value, "ClientExp=="+expiryTime, "IpLimit=="+ip_limit, "ClientComment=="+t.draft.client_Comment)

	case model.Shadowsocks:
		message = t.I18nBot("tgbot.messages.inbound_client_data_pass", "InboundRemark=="+inbound_remark, "ClientPass=="+t.draft.client_ShPassword, "ClientEmail=="+t.draft.client_Email, "ClientTraffic=="+traffic_value, "ClientExp=="+expiryTime, "IpLimit=="+ip_limit, "ClientComment=="+t.draft.client_Comment)

	default:
		return "", errors.New("unknown protocol")
	}

	return message, nil
}

func (t *Tgbot) BuildJSONForProtocol(protocol model.Protocol) (string, error) {
	var jsonString string

	switch protocol {
	case model.VMESS:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "id": "%s",
                "security": "%s",
                "email": "%s",
                "limitIp": %d,
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, t.draft.client_Id, t.draft.client_Security, t.draft.client_Email, t.draft.client_LimitIP, t.draft.client_TotalGB, t.draft.client_ExpiryTime, t.draft.client_Enable, t.draft.client_TgID, t.draft.client_SubID, t.draft.client_Comment, t.draft.client_Reset)

	case model.VLESS:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "id": "%s",
                "flow": "%s",
                "email": "%s",
                "limitIp": %d,
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, t.draft.client_Id, t.draft.client_Flow, t.draft.client_Email, t.draft.client_LimitIP, t.draft.client_TotalGB, t.draft.client_ExpiryTime, t.draft.client_Enable, t.draft.client_TgID, t.draft.client_SubID, t.draft.client_Comment, t.draft.client_Reset)

	case model.Trojan:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "password": "%s",
                "email": "%s",
                "limitIp": %d,
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, t.draft.client_TrPassword, t.draft.client_Email, t.draft.client_LimitIP, t.draft.client_TotalGB, t.draft.client_ExpiryTime, t.draft.client_Enable, t.draft.client_TgID, t.draft.client_SubID, t.draft.client_Comment, t.draft.client_Reset)

	case model.Shadowsocks:
		jsonString = fmt.Sprintf(`{
            "clients": [{
                "method": "%s",
                "password": "%s",
                "email": "%s",
                "limitIp": %d,
                "totalGB": %d,
                "expiryTime": %d,
                "enable": %t,
                "tgId": "%s",
                "subId": "%s",
                "comment": "%s",
                "reset": %d
            }]
        }`, t.draft.client_Method, t.draft.client_ShPassword, t.draft.client_Email, t.draft.client_LimitIP, t.draft.client_TotalGB, t.draft.client_ExpiryTime, t.draft.client_Enable, t.draft.client_TgID, t.draft.client_SubID, t.draft.client_Comment, t.draft.client_Reset)

	default:
		return "", errors.New("unknown protocol")
	}

	return jsonString, nil
}

func (t *Tgbot) SubmitAddClient() (bool, error) {

	inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
	if err != nil {
		logger.Warning("getIboundClients run failed:", err)
		return false, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	jsonString, err := t.BuildJSONForProtocol(inbound.Protocol)
	if err != nil {
		logger.Warning("BuildJSONForProtocol run failed:", err)
		return false, errors.New("failed to build JSON for protocol")
	}

	newInbound := &model.Inbound{
		Id:       t.draft.receiver_inbound_ID,
		Settings: jsonString,
	}

	return t.inboundService.AddInboundClient(newInbound)
}

func checkAdmin(tgId int64) bool {
	for _, adminId := range adminIds {
		if adminId == tgId {
			return true
		}
	}
	return false
}

func (t *Tgbot) SendAnswer(chatId int64, msg string, isAdmin bool) {
	if isAdmin {
		t.sendAdminGroup(chatId, "menu_root")
		return
	}
	numericKeyboardClient := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.clientUsage")).WithCallbackData(t.encodeQuery("client_traffic")),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.commands")).WithCallbackData(t.encodeQuery("client_commands")),
		),
	)

	t.SendMsgToTgbot(chatId, msg, numericKeyboardClient)
}

func (t *Tgbot) SendMsgToTgbot(chatId int64, msg string, replyMarkup ...telego.ReplyMarkup) {
	if !isRunning {
		return
	}

	if msg == "" {
		logger.Info("[tgbot] message is empty!")
		return
	}

	var allMessages []string
	limit := 2000

	// paging message if it is big
	if len(msg) > limit {
		messages := strings.Split(msg, "\r\n\r\n")
		lastIndex := -1

		for _, message := range messages {
			if (len(allMessages) == 0) || (len(allMessages[lastIndex])+len(message) > limit) {
				allMessages = append(allMessages, message)
				lastIndex++
			} else {
				allMessages[lastIndex] += "\r\n\r\n" + message
			}
		}
		if strings.TrimSpace(allMessages[len(allMessages)-1]) == "" {
			allMessages = allMessages[:len(allMessages)-1]
		}
	} else {
		allMessages = append(allMessages, msg)
	}
	for n, message := range allMessages {
		params := telego.SendMessageParams{
			ChatID:    tu.ID(chatId),
			Text:      message,
			ParseMode: "HTML",
		}
		// only add replyMarkup to last message
		if len(replyMarkup) > 0 && n == (len(allMessages)-1) {
			params.ReplyMarkup = secureKeyboard(chatId, replyMarkup[0])
		}
		sent, err := bot.SendMessage(context.Background(), &params)
		if err == nil && sent != nil && len(replyMarkup) > 0 {
			bindKeyboard(chatId, sent.MessageID, params.ReplyMarkup)
		}
		if err != nil {
			logger.Warning("Error sending telegram message :", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (t *Tgbot) SendMsgToTgbotAdmins(msg string, replyMarkup ...telego.ReplyMarkup) {
	if len(replyMarkup) > 0 {
		for _, adminId := range adminIds {
			t.SendMsgToTgbot(adminId, msg, replyMarkup[0])
		}
	} else {
		for _, adminId := range adminIds {
			t.SendMsgToTgbot(adminId, msg)
		}
	}
}

// SendReport sends one local operations summary per configured administrator.
func (t *Tgbot) SendReport() {
	if !t.IsRunning() {
		return
	}
	settings := t.settingService
	if settings == nil {
		settings = &SettingService{}
	}
	location, err := settings.GetTimeLocation()
	if err != nil || location == nil {
		location = time.Local
	}
	server := t.serverService
	if server == nil {
		server = &ServerService{}
	}
	status := server.getStatus(nil, false)
	host, err := os.Hostname()
	if err != nil {
		host = "不可用"
	}
	msg := fmt.Sprintf("<b>管理员运营摘要</b>\n时间：%s（时区：%s）\n主机：%s\n面板版本：%s\n服务器状态：运行时长 %s；CPU %.1f%%\n内存：%s / %s\n磁盘：%s / %s\nTCP / UDP：%d / %d\n主机累计网络流量：上传 %s；下载 %s\nXray 状态：%s；版本：%s",
		time.Now().In(location).Format("2006-01-02 15:04:05 MST -0700"), html.EscapeString(location.String()),
		html.EscapeString(host), html.EscapeString(config.GetVersion()), time.Duration(status.Uptime)*time.Second,
		status.Cpu, common.FormatTraffic(int64(status.Mem.Current)), common.FormatTraffic(int64(status.Mem.Total)),
		common.FormatTraffic(int64(status.Disk.Current)), common.FormatTraffic(int64(status.Disk.Total)),
		status.TcpCount, status.UdpCount, common.FormatTraffic(int64(status.NetTraffic.Sent)),
		common.FormatTraffic(int64(status.NetTraffic.Recv)), html.EscapeString(string(status.Xray.State)), html.EscapeString(status.Xray.Version))
	t.SendMsgToTgbotAdmins(msg)
}

func (t *Tgbot) SendBackupToAdmins() {
	if !t.IsRunning() {
		return
	}
	for _, adminId := range adminIds {
		t.sendBackup(int64(adminId))
	}
}

func (t *Tgbot) sendExhaustedToAdmins() {
	if !t.IsRunning() {
		return
	}
	for _, adminId := range adminIds {
		t.getExhausted(int64(adminId))
	}
}

func (t *Tgbot) getServerUsage(chatId int64, messageID ...int) string {
	info := t.prepareServerUsageInfo()

	keyboard := tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("usage_refresh"))))

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], info, keyboard)
	} else {
		t.SendMsgToTgbot(chatId, info, keyboard)
	}

	return info
}

// Send server usage without an inline keyboard
func (t *Tgbot) sendServerUsage() string {
	info := t.prepareServerUsageInfo()
	return info
}

func (t *Tgbot) prepareServerUsageInfo() string {
	info, ipv4, ipv6 := "", "", ""

	// get latest status of server
	t.lastStatus = t.serverService.GetStatus(t.lastStatus)
	onlines := p.GetOnlineClients()

	info += t.I18nBot("tgbot.messages.hostname", "Hostname=="+hostname)
	info += t.I18nBot("tgbot.messages.version", "Version=="+config.GetVersion())
	info += t.I18nBot("tgbot.messages.xrayVersion", "XrayVersion=="+fmt.Sprint(t.lastStatus.Xray.Version))

	// get ip address
	netInterfaces, err := net.Interfaces()
	if err != nil {
		logger.Error("net.Interfaces failed, err: ", err.Error())
		info += t.I18nBot("tgbot.messages.ip", "IP=="+t.I18nBot("tgbot.unknown"))
		info += "\r\n"
	} else {
		for i := 0; i < len(netInterfaces); i++ {
			if (netInterfaces[i].Flags & net.FlagUp) != 0 {
				addrs, _ := netInterfaces[i].Addrs()

				for _, address := range addrs {
					if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
						if ipnet.IP.To4() != nil {
							ipv4 += ipnet.IP.String() + " "
						} else if ipnet.IP.To16() != nil && !ipnet.IP.IsLinkLocalUnicast() {
							ipv6 += ipnet.IP.String() + " "
						}
					}
				}
			}
		}

		info += t.I18nBot("tgbot.messages.ipv4", "IPv4=="+ipv4)
		info += t.I18nBot("tgbot.messages.ipv6", "IPv6=="+ipv6)
	}

	info += t.I18nBot("tgbot.messages.serverUpTime", "UpTime=="+strconv.FormatUint(t.lastStatus.Uptime/86400, 10), "Unit=="+t.I18nBot("tgbot.days"))
	info += t.I18nBot("tgbot.messages.serverLoad", "Load1=="+strconv.FormatFloat(t.lastStatus.Loads[0], 'f', 2, 64), "Load2=="+strconv.FormatFloat(t.lastStatus.Loads[1], 'f', 2, 64), "Load3=="+strconv.FormatFloat(t.lastStatus.Loads[2], 'f', 2, 64))
	info += t.I18nBot("tgbot.messages.serverMemory", "Current=="+common.FormatTraffic(int64(t.lastStatus.Mem.Current)), "Total=="+common.FormatTraffic(int64(t.lastStatus.Mem.Total)))
	info += t.I18nBot("tgbot.messages.onlinesCount", "Count=="+fmt.Sprint(len(onlines)))
	info += t.I18nBot("tgbot.messages.tcpCount", "Count=="+strconv.Itoa(t.lastStatus.TcpCount))
	info += t.I18nBot("tgbot.messages.udpCount", "Count=="+strconv.Itoa(t.lastStatus.UdpCount))
	info += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic(int64(t.lastStatus.NetTraffic.Sent+t.lastStatus.NetTraffic.Recv)), "Upload=="+common.FormatTraffic(int64(t.lastStatus.NetTraffic.Sent)), "Download=="+common.FormatTraffic(int64(t.lastStatus.NetTraffic.Recv)))
	info += t.I18nBot("tgbot.messages.xrayStatus", "State=="+fmt.Sprint(t.lastStatus.Xray.State))
	return info
}

func (t *Tgbot) UserLoginNotify(username string, ip string, time string, status LoginStatus) {
	if !t.IsRunning() {
		return
	}

	if username == "" || ip == "" || time == "" {
		logger.Warning("UserLoginNotify failed, invalid info!")
		return
	}

	loginNotifyEnabled, err := t.settingService.GetTgBotLoginNotify()
	if err != nil || !loginNotifyEnabled {
		return
	}

	msg := ""
	switch status {
	case LoginSuccess:
		msg += t.I18nBot("tgbot.messages.loginSuccess")
		msg += t.I18nBot("tgbot.messages.hostname", "Hostname=="+hostname)
	case LoginFail:
		msg += t.I18nBot("tgbot.messages.loginFailed")
		msg += t.I18nBot("tgbot.messages.hostname", "Hostname=="+hostname)
	}
	msg += t.I18nBot("tgbot.messages.username", "Username=="+username)
	msg += t.I18nBot("tgbot.messages.ip", "IP=="+ip)
	msg += t.I18nBot("tgbot.messages.time", "Time=="+time)
	t.SendMsgToTgbotAdmins(msg)
}

func (t *Tgbot) getInboundUsages() string {
	info := ""
	// get traffic
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		info += t.I18nBot("tgbot.answers.getInboundsFailed")
	} else {
		// NOTE:If there no any sessions here,need to notify here
		// TODO:Sub-node push, automatic conversion format
		for _, inbound := range inbounds {
			info += t.I18nBot("tgbot.messages.inbound", "Remark=="+inbound.Remark)
			info += t.I18nBot("tgbot.messages.port", "Port=="+strconv.Itoa(inbound.Port))
			info += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic((inbound.Up+inbound.Down)), "Upload=="+common.FormatTraffic(inbound.Up), "Download=="+common.FormatTraffic(inbound.Down))

			if inbound.ExpiryTime == 0 {
				info += t.I18nBot("tgbot.messages.expire", "Time=="+t.I18nBot("tgbot.unlimited"))
			} else {
				info += t.I18nBot("tgbot.messages.expire", "Time=="+time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
			}
			info += "\r\n"
		}
	}
	return info
}
func (t *Tgbot) getInbounds() (*telego.InlineKeyboardMarkup, error) {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	if len(inbounds) == 0 {
		logger.Warning("No inbounds found")
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	var buttons []telego.InlineKeyboardButton
	for _, inbound := range inbounds {
		status := "❌"
		if inbound.Enable {
			status = "✅"
		}
		callbackData := t.encodeQuery(fmt.Sprintf("%s %d", "get_clients", inbound.Id))
		buttons = append(buttons, tu.InlineKeyboardButton(fmt.Sprintf("%v - %v", inbound.Remark, status)).WithCallbackData(callbackData))
	}

	cols := 1
	if len(buttons) >= 6 {
		cols = 2
	}

	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
	return keyboard, nil
}

func (t *Tgbot) getInboundsAddClient() (*telego.InlineKeyboardMarkup, error) {
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("GetAllInbounds run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	if len(inbounds) == 0 {
		logger.Warning("No inbounds found")
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}

	excludedProtocols := map[model.Protocol]bool{
		model.Tunnel:    true,
		model.Socks:     true,
		model.WireGuard: true,
		model.HTTP:      true,
	}

	var buttons []telego.InlineKeyboardButton
	for _, inbound := range inbounds {
		if excludedProtocols[inbound.Protocol] {
			continue
		}

		status := "❌"
		if inbound.Enable {
			status = "✅"
		}
		callbackData := t.encodeQuery(fmt.Sprintf("%s %d", "add_client_to", inbound.Id))
		buttons = append(buttons, tu.InlineKeyboardButton(fmt.Sprintf("%v - %v", inbound.Remark, status)).WithCallbackData(callbackData))
	}

	cols := 1
	if len(buttons) >= 6 {
		cols = 2
	}

	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
	return keyboard, nil
}

func (t *Tgbot) getInboundClients(id int) (*telego.InlineKeyboardMarkup, error) {
	inbound, err := t.inboundService.GetInbound(id)
	if err != nil {
		logger.Warning("getIboundClients run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	}
	clients, err := t.inboundService.GetClients(inbound)
	var buttons []telego.InlineKeyboardButton

	if err != nil {
		logger.Warning("GetInboundClients run failed:", err)
		return nil, errors.New(t.I18nBot("tgbot.answers.getInboundsFailed"))
	} else {
		if len(clients) > 0 {
			for _, client := range clients {
				buttons = append(buttons, tu.InlineKeyboardButton(client.Email).WithCallbackData(t.encodeQuery("client_get_usage "+client.Email)))
			}

		} else {
			return nil, errors.New(t.I18nBot("tgbot.answers.getClientsFailed"))
		}

	}
	cols := 0
	if len(buttons) < 6 {
		cols = 3
	} else {
		cols = 2
	}
	keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))

	return keyboard, nil
}

func (t *Tgbot) clientInfoMsg(
	traffic *xray.ClientTraffic,
	printEnabled bool,
	printOnline bool,
	printActive bool,
	printDate bool,
	printTraffic bool,
	printRefreshed bool,
) string {
	now := time.Now().Unix()
	expiryTime := ""
	flag := false
	diff := traffic.ExpiryTime/1000 - now
	if traffic.ExpiryTime == 0 {
		expiryTime = t.I18nBot("tgbot.unlimited")
	} else if diff > 172800 || !traffic.Enable {
		expiryTime = time.Unix((traffic.ExpiryTime / 1000), 0).Format("2006-01-02 15:04:05")
		if diff > 0 {
			days := diff / 86400
			hours := (diff % 86400) / 3600
			minutes := (diff % 3600) / 60
			remainingTime := ""
			if days > 0 {
				remainingTime += fmt.Sprintf("%d %s ", days, t.I18nBot("tgbot.days"))
			}
			if hours > 0 {
				remainingTime += fmt.Sprintf("%d %s ", hours, t.I18nBot("tgbot.hours"))
			}
			if minutes > 0 {
				remainingTime += fmt.Sprintf("%d %s", minutes, t.I18nBot("tgbot.minutes"))
			}
			expiryTime += fmt.Sprintf(" (%s)", remainingTime)
		}
	} else if traffic.ExpiryTime < 0 {
		expiryTime = fmt.Sprintf("%d %s", traffic.ExpiryTime/-86400000, t.I18nBot("tgbot.days"))
		flag = true
	} else {
		expiryTime = fmt.Sprintf("%d %s", diff/3600, t.I18nBot("tgbot.hours"))
		flag = true
	}

	total := ""
	if traffic.Total == 0 {
		total = t.I18nBot("tgbot.unlimited")
	} else {
		total = common.FormatTraffic((traffic.Total))
	}

	enabled := ""
	isEnabled, err := t.inboundService.checkIsEnabledByEmail(traffic.Email)
	if err != nil {
		logger.Warning(err)
		enabled = t.I18nBot("tgbot.wentWrong")
	} else if isEnabled {
		enabled = t.I18nBot("tgbot.messages.yes")
	} else {
		enabled = t.I18nBot("tgbot.messages.no")
	}

	active := ""
	if traffic.Enable {
		active = t.I18nBot("tgbot.messages.yes")
	} else {
		active = t.I18nBot("tgbot.messages.no")
	}

	status := t.I18nBot("tgbot.offline")
	if p != nil && p.IsRunning() {
		for _, online := range p.GetOnlineClients() {
			if online == traffic.Email {
				status = t.I18nBot("tgbot.online")
				break
			}
		}
	}

	output := ""
	output += t.I18nBot("tgbot.messages.email", "Email=="+traffic.Email)
	if printEnabled {
		output += t.I18nBot("tgbot.messages.enabled", "Enable=="+enabled)
	}
	if printOnline {
		output += t.I18nBot("tgbot.messages.online", "Status=="+status)
	}
	if printActive {
		output += t.I18nBot("tgbot.messages.active", "Enable=="+active)
	}
	if printDate {
		if flag {
			output += t.I18nBot("tgbot.messages.expireIn", "Time=="+expiryTime)
		} else {
			output += t.I18nBot("tgbot.messages.expire", "Time=="+expiryTime)
		}
	}
	if printTraffic {
		output += t.I18nBot("tgbot.messages.upload", "Upload=="+common.FormatTraffic(traffic.Up))
		output += t.I18nBot("tgbot.messages.download", "Download=="+common.FormatTraffic(traffic.Down))
		output += t.I18nBot("tgbot.messages.total", "UpDown=="+common.FormatTraffic((traffic.Up+traffic.Down)), "Total=="+total)
	}
	if printRefreshed {
		output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	}

	return output
}

func (t *Tgbot) getClientUsage(chatId int64, tgUserID int64, email ...string) {
	traffics, err := t.inboundService.GetClientTrafficTgBot(tgUserID)
	if err != nil {
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		t.SendMsgToTgbot(chatId, msg)
		return
	}

	if len(traffics) == 0 {
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.answers.askToAddUserId", "TgUserID=="+strconv.FormatInt(tgUserID, 10)))
		return
	}

	output := ""

	if len(traffics) > 0 {
		if len(email) > 0 {
			for _, traffic := range traffics {
				if traffic.Email == email[0] {
					output := t.clientInfoMsg(traffic, true, true, true, true, true, true)
					t.SendMsgToTgbot(chatId, output)
					return
				}
			}
			msg := t.I18nBot("tgbot.noResult")
			t.SendMsgToTgbot(chatId, msg)
			return
		} else {
			for _, traffic := range traffics {
				output += t.clientInfoMsg(traffic, true, true, true, true, true, false)
				output += "\r\n"
			}
		}
	}

	output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	t.SendMsgToTgbot(chatId, output)
	output = t.I18nBot("tgbot.commands.pleaseChoose")
	t.SendAnswer(chatId, output, false)
}

func (t *Tgbot) searchClientIps(chatId int64, email string, messageID ...int) {
	ips, err := t.inboundService.GetInboundClientIps(email)
	if err != nil || len(ips) == 0 {
		ips = t.I18nBot("tgbot.noIpRecord")
	}

	output := ""
	output += t.I18nBot("tgbot.messages.email", "Email=="+email)
	output += t.I18nBot("tgbot.messages.ips", "IPs=="+ips)
	output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	inlineKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("ips_refresh "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.clearIPs")).WithCallbackData(t.encodeQuery("clear_ips "+email)),
		),
	)

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, inlineKeyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, inlineKeyboard)
	}
}

func (t *Tgbot) clientTelegramUserInfo(chatId int64, email string, messageID ...int) {
	traffic, client, err := t.inboundService.GetClientByEmail(email)
	if err != nil {
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		t.SendMsgToTgbot(chatId, msg)
		return
	}
	if client == nil {
		msg := t.I18nBot("tgbot.noResult")
		t.SendMsgToTgbot(chatId, msg)
		return
	}
	tgId := "None"
	if client.TgID != 0 {
		tgId = strconv.FormatInt(client.TgID, 10)
	}

	output := ""
	output += t.I18nBot("tgbot.messages.email", "Email=="+email)
	output += t.I18nBot("tgbot.messages.TGUser", "TelegramID=="+tgId)
	output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))

	inlineKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("tgid_refresh "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.removeTGUser")).WithCallbackData(t.encodeQuery("tgid_remove "+email)),
		),
	)

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, inlineKeyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, inlineKeyboard)
		delete(bindingRequests, chatId)
		requestID, err := reserveTelegramBindingID()
		if err != nil {
			logger.Warning("Telegram binding request reservation failed: ", err)
			t.SendMsgToTgbot(chatId, "Cannot safely issue a user-binding request. Please retry or contact the administrator.", tu.ReplyKeyboardRemove())
			return
		}
		bindingRequests[chatId] = struct {
			id     int32
			client int
			until  time.Time
		}{requestID, traffic.Id, time.Now().Add(20 * time.Minute)}
		requestUser := telego.KeyboardButtonRequestUsers{
			RequestID: requestID,
			UserIsBot: new(bool),
		}
		keyboard := tu.Keyboard(
			tu.KeyboardRow(
				tu.KeyboardButton(t.I18nBot("tgbot.buttons.selectTGUser")).WithRequestUsers(&requestUser),
			),
			tu.KeyboardRow(
				tu.KeyboardButton(t.I18nBot("tgbot.buttons.closeKeyboard")),
			),
		).WithIsPersistent().WithResizeKeyboard()
		t.SendMsgToTgbot(chatId, t.I18nBot("tgbot.buttons.selectOneTGUser"), keyboard)
	}
}

func (t *Tgbot) searchClient(chatId int64, email string, messageID ...int) {
	traffic, err := t.inboundService.GetClientTrafficByEmail(email)
	if err != nil {
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		t.SendMsgToTgbot(chatId, msg)
		return
	}
	if traffic == nil {
		msg := t.I18nBot("tgbot.noResult")
		t.SendMsgToTgbot(chatId, msg)
		return
	}

	output := t.clientInfoMsg(traffic, true, true, true, true, true, true)

	inlineKeyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("client_refresh "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetTraffic")).WithCallbackData(t.encodeQuery("reset_traffic "+email)),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData(t.encodeQuery("limit_traffic "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData(t.encodeQuery("reset_exp "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.ipLog")).WithCallbackData(t.encodeQuery("ip_log "+email)),
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.ipLimit")).WithCallbackData(t.encodeQuery("ip_limit "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.setTGUser")).WithCallbackData(t.encodeQuery("tg_user "+email)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.toggle")).WithCallbackData(t.encodeQuery("toggle_enable "+email)),
		),
	)
	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, inlineKeyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, inlineKeyboard)
	}
}

func (t *Tgbot) addClient(chatId int64, msg string, messageID ...int) {
	inbound, err := t.inboundService.GetInbound(t.draft.receiver_inbound_ID)
	if err != nil {
		t.SendMsgToTgbot(chatId, err.Error())
		return
	}

	protocol := inbound.Protocol

	switch protocol {
	case model.VMESS, model.VLESS:
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_email")).WithCallbackData("add_client_ch_default_email"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_id")).WithCallbackData("add_client_ch_default_id"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData("add_client_ch_default_traffic"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData("add_client_ch_default_exp"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_comment")).WithCallbackData("add_client_ch_default_comment"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.ipLimit")).WithCallbackData("add_client_ch_default_ip_limit"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitDisable")).WithCallbackData("add_client_submit_disable"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitEnable")).WithCallbackData("add_client_submit_enable"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("add_client_cancel"),
			),
		)
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	case model.Trojan:
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_email")).WithCallbackData("add_client_ch_default_email"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_password")).WithCallbackData("add_client_ch_default_pass_tr"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData("add_client_ch_default_traffic"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData("add_client_ch_default_exp"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_comment")).WithCallbackData("add_client_ch_default_comment"),
				tu.InlineKeyboardButton("ip limit").WithCallbackData("add_client_ch_default_ip_limit"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitDisable")).WithCallbackData("add_client_submit_disable"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitEnable")).WithCallbackData("add_client_submit_enable"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("add_client_cancel"),
			),
		)
		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	case model.Shadowsocks:
		inlineKeyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_email")).WithCallbackData("add_client_ch_default_email"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_password")).WithCallbackData("add_client_ch_default_pass_sh"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.limitTraffic")).WithCallbackData("add_client_ch_default_traffic"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.resetExpire")).WithCallbackData("add_client_ch_default_exp"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.change_comment")).WithCallbackData("add_client_ch_default_comment"),
				tu.InlineKeyboardButton("ip limit").WithCallbackData("add_client_ch_default_ip_limit"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitDisable")).WithCallbackData("add_client_submit_disable"),
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.submitEnable")).WithCallbackData("add_client_submit_enable"),
			),
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.cancel")).WithCallbackData("add_client_cancel"),
			),
		)

		if len(messageID) > 0 {
			t.editMessageTgBot(chatId, messageID[0], msg, inlineKeyboard)
		} else {
			t.SendMsgToTgbot(chatId, msg, inlineKeyboard)
		}
	}

}

func (t *Tgbot) searchInbound(chatId int64, remark string) {
	inbounds, err := t.inboundService.SearchInbounds(remark)
	if err != nil {
		logger.Warning(err)
		msg := t.I18nBot("tgbot.wentWrong")
		t.SendMsgToTgbot(chatId, msg)
		return
	}
	if len(inbounds) == 0 {
		msg := t.I18nBot("tgbot.noInbounds")
		t.SendMsgToTgbot(chatId, msg)
		return
	}

	for _, inbound := range inbounds {
		info := ""
		info += t.I18nBot("tgbot.messages.inbound", "Remark=="+inbound.Remark)
		info += t.I18nBot("tgbot.messages.port", "Port=="+strconv.Itoa(inbound.Port))
		info += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic((inbound.Up+inbound.Down)), "Upload=="+common.FormatTraffic(inbound.Up), "Download=="+common.FormatTraffic(inbound.Down))

		if inbound.ExpiryTime == 0 {
			info += t.I18nBot("tgbot.messages.expire", "Time=="+t.I18nBot("tgbot.unlimited"))
		} else {
			info += t.I18nBot("tgbot.messages.expire", "Time=="+time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
		}
		t.SendMsgToTgbot(chatId, info)

		if len(inbound.ClientStats) > 0 {
			output := ""
			for _, traffic := range inbound.ClientStats {
				output += t.clientInfoMsg(&traffic, true, true, true, true, true, true)
			}
			t.SendMsgToTgbot(chatId, output)
		}
	}
}

func (t *Tgbot) getExhausted(chatId int64) {
	trDiff := int64(0)
	exDiff := int64(0)
	now := time.Now().Unix() * 1000
	var exhaustedInbounds []model.Inbound
	var exhaustedClients []xray.ClientTraffic
	var disabledInbounds []model.Inbound
	var disabledClients []xray.ClientTraffic

	TrafficThreshold, err := t.settingService.GetTrafficDiff()
	if err == nil && TrafficThreshold > 0 {
		trDiff = int64(TrafficThreshold) * 1073741824
	}
	ExpireThreshold, err := t.settingService.GetExpireDiff()
	if err == nil && ExpireThreshold > 0 {
		exDiff = int64(ExpireThreshold) * 86400000
	}
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Unable to load Inbounds", err)
	}

	for _, inbound := range inbounds {
		if inbound.Enable {
			if (inbound.ExpiryTime > 0 && (inbound.ExpiryTime-now < exDiff)) ||
				(inbound.Total > 0 && (inbound.Total-(inbound.Up+inbound.Down) < trDiff)) {
				exhaustedInbounds = append(exhaustedInbounds, *inbound)
			}
			if len(inbound.ClientStats) > 0 {
				for _, client := range inbound.ClientStats {
					if client.Enable {
						if (client.ExpiryTime > 0 && (client.ExpiryTime-now < exDiff)) ||
							(client.Total > 0 && (client.Total-(client.Up+client.Down) < trDiff)) {
							exhaustedClients = append(exhaustedClients, client)
						}
					} else {
						disabledClients = append(disabledClients, client)
					}
				}
			}
		} else {
			disabledInbounds = append(disabledInbounds, *inbound)
		}
	}

	// Inbounds
	output := ""
	output += t.I18nBot("tgbot.messages.exhaustedCount", "Type=="+t.I18nBot("tgbot.inbounds"))
	output += t.I18nBot("tgbot.messages.disabled", "Disabled=="+strconv.Itoa(len(disabledInbounds)))
	output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+strconv.Itoa(len(exhaustedInbounds)))

	if len(exhaustedInbounds) > 0 {
		output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+t.I18nBot("tgbot.inbounds"))

		for _, inbound := range exhaustedInbounds {
			output += t.I18nBot("tgbot.messages.inbound", "Remark=="+inbound.Remark)
			output += t.I18nBot("tgbot.messages.port", "Port=="+strconv.Itoa(inbound.Port))
			output += t.I18nBot("tgbot.messages.traffic", "Total=="+common.FormatTraffic((inbound.Up+inbound.Down)), "Upload=="+common.FormatTraffic(inbound.Up), "Download=="+common.FormatTraffic(inbound.Down))
			if inbound.ExpiryTime == 0 {
				output += t.I18nBot("tgbot.messages.expire", "Time=="+t.I18nBot("tgbot.unlimited"))
			} else {
				output += t.I18nBot("tgbot.messages.expire", "Time=="+time.Unix((inbound.ExpiryTime/1000), 0).Format("2006-01-02 15:04:05"))
			}
			output += "\r\n"
		}
	}

	// Clients
	exhaustedCC := len(exhaustedClients)
	output += t.I18nBot("tgbot.messages.exhaustedCount", "Type=="+t.I18nBot("tgbot.clients"))
	output += t.I18nBot("tgbot.messages.disabled", "Disabled=="+strconv.Itoa(len(disabledClients)))
	output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+strconv.Itoa(exhaustedCC))

	if exhaustedCC > 0 {
		output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+t.I18nBot("tgbot.clients"))
		var buttons []telego.InlineKeyboardButton
		for _, traffic := range exhaustedClients {
			output += t.clientInfoMsg(&traffic, true, false, false, true, true, false)
			output += "\r\n"
			buttons = append(buttons, tu.InlineKeyboardButton(traffic.Email).WithCallbackData(t.encodeQuery("client_get_usage "+traffic.Email)))
		}
		cols := 0
		if exhaustedCC < 11 {
			cols = 1
		} else {
			cols = 2
		}
		output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
		keyboard := tu.InlineKeyboardGrid(tu.InlineKeyboardCols(cols, buttons...))
		t.SendMsgToTgbot(chatId, output, keyboard)
	} else {
		output += t.I18nBot("tgbot.messages.refreshedOn", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
		t.SendMsgToTgbot(chatId, output)
	}
}

func (t *Tgbot) notifyExhausted() {
	trDiff := int64(0)
	exDiff := int64(0)
	now := time.Now().Unix() * 1000

	TrafficThreshold, err := t.settingService.GetTrafficDiff()
	if err == nil && TrafficThreshold > 0 {
		trDiff = int64(TrafficThreshold) * 1073741824
	}
	ExpireThreshold, err := t.settingService.GetExpireDiff()
	if err == nil && ExpireThreshold > 0 {
		exDiff = int64(ExpireThreshold) * 86400000
	}
	inbounds, err := t.inboundService.GetAllInbounds()
	if err != nil {
		logger.Warning("Unable to load Inbounds", err)
	}

	var chatIDsDone []int64
	for _, inbound := range inbounds {
		if inbound.Enable {
			if len(inbound.ClientStats) > 0 {
				clients, err := t.inboundService.GetClients(inbound)
				if err == nil {
					for _, client := range clients {
						if client.TgID != 0 {
							chatID := client.TgID
							if !int64Contains(chatIDsDone, chatID) && !checkAdmin(chatID) {
								var disabledClients []xray.ClientTraffic
								var exhaustedClients []xray.ClientTraffic
								traffics, err := t.inboundService.GetClientTrafficTgBot(client.TgID)
								if err == nil && len(traffics) > 0 {
									output := t.I18nBot("tgbot.messages.exhaustedCount", "Type=="+t.I18nBot("tgbot.clients"))
									for _, traffic := range traffics {
										if traffic.Enable {
											if (traffic.ExpiryTime > 0 && (traffic.ExpiryTime-now < exDiff)) ||
												(traffic.Total > 0 && (traffic.Total-(traffic.Up+traffic.Down) < trDiff)) {
												exhaustedClients = append(exhaustedClients, *traffic)
											}
										} else {
											disabledClients = append(disabledClients, *traffic)
										}
									}
									if len(exhaustedClients) > 0 {
										output += t.I18nBot("tgbot.messages.disabled", "Disabled=="+strconv.Itoa(len(disabledClients)))
										if len(disabledClients) > 0 {
											output += t.I18nBot("tgbot.clients") + ":\r\n"
											for _, traffic := range disabledClients {
												output += " " + traffic.Email
											}
											output += "\r\n"
										}
										output += "\r\n"
										output += t.I18nBot("tgbot.messages.depleteSoon", "Deplete=="+strconv.Itoa(len(exhaustedClients)))
										for _, traffic := range exhaustedClients {
											output += t.clientInfoMsg(&traffic, true, false, false, true, true, false)
											output += "\r\n"
										}
										t.SendMsgToTgbot(chatID, output)
									}
									chatIDsDone = append(chatIDsDone, chatID)
								}
							}
						}
					}
				}
			}
		}
	}
}

func int64Contains(slice []int64, item int64) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (t *Tgbot) onlineClients(chatId int64, messageID ...int) {
	if !p.IsRunning() {
		return
	}

	onlines := p.GetOnlineClients()
	onlinesCount := len(onlines)
	output := t.I18nBot("tgbot.messages.onlinesCount", "Count=="+fmt.Sprint(onlinesCount))
	keyboard := tu.InlineKeyboard(tu.InlineKeyboardRow(
		tu.InlineKeyboardButton(t.I18nBot("tgbot.buttons.refresh")).WithCallbackData(t.encodeQuery("onlines_refresh"))))

	if onlinesCount > 0 {
		var buttons []telego.InlineKeyboardButton
		for _, online := range onlines {
			buttons = append(buttons, tu.InlineKeyboardButton(online).WithCallbackData(t.encodeQuery("client_get_usage "+online)))
		}
		cols := 0
		if onlinesCount < 21 {
			cols = 2
		} else if onlinesCount < 61 {
			cols = 3
		} else {
			cols = 4
		}
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tu.InlineKeyboardCols(cols, buttons...)...)
	}

	if len(messageID) > 0 {
		t.editMessageTgBot(chatId, messageID[0], output, keyboard)
	} else {
		t.SendMsgToTgbot(chatId, output, keyboard)
	}
}

func (t *Tgbot) sendBackup(chatId int64) {
	output := t.I18nBot("tgbot.messages.backupTime", "Time=="+time.Now().Format("2006-01-02 15:04:05"))
	t.SendMsgToTgbot(chatId, output)

	// Update by manually trigger a checkpoint operation
	err := database.Checkpoint()
	if err != nil {
		logger.Error("Error in trigger a checkpoint operation: ", err)
	}

	file, err := os.Open(config.GetDBPath())
	if err == nil {
		document := tu.Document(
			tu.ID(chatId),
			tu.File(file),
		)
		_, err = bot.SendDocument(context.Background(), document)
		if err != nil {
			logger.Error("Error in uploading backup: ", err)
		}
	} else {
		logger.Error("Error in opening db file for backup: ", err)
	}

	file, err = os.Open(xray.GetConfigPath())
	if err == nil {
		document := tu.Document(
			tu.ID(chatId),
			tu.File(file),
		)
		_, err = bot.SendDocument(context.Background(), document)
		if err != nil {
			logger.Error("Error in uploading config.json: ", err)
		}
	} else {
		logger.Error("Error in opening config.json file for backup: ", err)
	}
}

func (t *Tgbot) sendBanLogs(chatId int64, dt bool) {
	if dt {
		output := t.I18nBot("tgbot.messages.datetime", "DateTime=="+time.Now().Format("2006-01-02 15:04:05"))
		t.SendMsgToTgbot(chatId, output)
	}

	file, err := os.Open(xray.GetIPLimitBannedPrevLogPath())
	if err == nil {
		// Check if the file is non-empty before attempting to upload
		fileInfo, _ := file.Stat()
		if fileInfo.Size() > 0 {
			document := tu.Document(
				tu.ID(chatId),
				tu.File(file),
			)
			_, err = bot.SendDocument(context.Background(), document)
			if err != nil {
				logger.Error("Error in uploading IPLimitBannedPrevLog: ", err)
			}
		} else {
			logger.Warning("IPLimitBannedPrevLog file is empty, not uploading.")
		}
		file.Close()
	} else {
		logger.Error("Error in opening IPLimitBannedPrevLog file for backup: ", err)
	}

	file, err = os.Open(xray.GetIPLimitBannedLogPath())
	if err == nil {
		// Check if the file is non-empty before attempting to upload
		fileInfo, _ := file.Stat()
		if fileInfo.Size() > 0 {
			document := tu.Document(
				tu.ID(chatId),
				tu.File(file),
			)
			_, err = bot.SendDocument(context.Background(), document)
			if err != nil {
				logger.Error("Error in uploading IPLimitBannedLog: ", err)
			}
		} else {
			logger.Warning("IPLimitBannedLog file is empty, not uploading.")
		}
		file.Close()
	} else {
		logger.Error("Error in opening IPLimitBannedLog file for backup: ", err)
	}
}

func (t *Tgbot) sendCallbackAnswerTgBot(id string, message string) {
	params := telego.AnswerCallbackQueryParams{
		CallbackQueryID: id,
		Text:            message,
	}
	if err := bot.AnswerCallbackQuery(context.Background(), &params); err != nil {
		logger.Warning(err)
	}
}

func (t *Tgbot) editMessageCallbackTgBot(chatId int64, messageID int, inlineKeyboard *telego.InlineKeyboardMarkup) {
	params := telego.EditMessageReplyMarkupParams{
		ChatID:      tu.ID(chatId),
		MessageID:   messageID,
		ReplyMarkup: secureKeyboard(chatId, inlineKeyboard).(*telego.InlineKeyboardMarkup),
	}
	if _, err := bot.EditMessageReplyMarkup(context.Background(), &params); err != nil {
		logger.Warning(err)
	} else {
		bindKeyboard(chatId, messageID, params.ReplyMarkup)
	}
}

func (t *Tgbot) editMessageTgBot(chatId int64, messageID int, text string, inlineKeyboard ...*telego.InlineKeyboardMarkup) {
	params := telego.EditMessageTextParams{
		ChatID:    tu.ID(chatId),
		MessageID: messageID,
		Text:      text,
		ParseMode: "HTML",
	}
	if len(inlineKeyboard) > 0 {
		params.ReplyMarkup = secureKeyboard(chatId, inlineKeyboard[0]).(*telego.InlineKeyboardMarkup)
	}
	if _, err := bot.EditMessageText(context.Background(), &params); err != nil {
		logger.Warning(err)
	} else if len(inlineKeyboard) > 0 {
		bindKeyboard(chatId, messageID, params.ReplyMarkup)
	}
}

func (t *Tgbot) SendMsgToTgbotDeleteAfter(chatId int64, msg string, delayInSeconds int, replyMarkup ...telego.ReplyMarkup) {
	// Determine if replyMarkup was passed; otherwise, set it to nil
	var replyMarkupParam telego.ReplyMarkup
	if len(replyMarkup) > 0 {
		replyMarkupParam = replyMarkup[0] // Use the first element
	}

	// Send the message
	sentMsg, err := bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatId),
		Text:        msg,
		ReplyMarkup: replyMarkupParam, // Use the correct replyMarkup value
	})
	if err != nil {
		logger.Warning("Failed to send message:", err)
		return
	}

	// Delete the sent message after the specified number of seconds
	owner := t
	if t.deleteOwner != nil {
		owner = t.deleteOwner
	}
	owner.deferredDeletes.Add(1)
	go func() {
		defer owner.deferredDeletes.Done()
		time.Sleep(time.Duration(delayInSeconds) * time.Second) // Wait for the specified delay
		t.deleteMessageTgBot(chatId, sentMsg.MessageID)         // Delete the message
	}()
}

func (t *Tgbot) deleteMessageTgBot(chatId int64, messageID int) {
	callbackRegistryMu.Lock()
	for token, record := range issuedCallbacks {
		if record.actor == chatId && record.message == messageID {
			delete(issuedCallbacks, token)
		}
	}
	callbackRegistryMu.Unlock()
	params := telego.DeleteMessageParams{
		ChatID:    tu.ID(chatId),
		MessageID: messageID,
	}
	if err := bot.DeleteMessage(context.Background(), &params); err != nil {
		logger.Warning("Failed to delete message:", err)
	} else {
		logger.Info("Message deleted successfully")
	}
}

func (t *Tgbot) isSingleWord(text string) bool {
	text = strings.TrimSpace(text)
	re := regexp.MustCompile(`\s+`)
	return re.MatchString(text)
}

// 〔中文注释〕: 新增方法，实现 TelegramService 接口。
// 当设备限制任务需要发送消息时，会调用此方法。
// 该方法内部调用了已有的 SendMsgToTgbotAdmins 函数，将消息发送给所有管理员。
func (t *Tgbot) SendMessage(msg string) error {
	if !t.IsRunning() {
		// 〔中文注释〕: 如果 Bot 未运行，返回错误，防止程序出错。
		return errors.New("Telegram bot is not running")
	}
	// 〔中文注释〕: 调用现有方法将消息发送给所有已配置的管理员。
	t.SendMsgToTgbotAdmins(msg)
	return nil
}

// 【新增函数】: 检查并安装【订阅转换】
func (t *Tgbot) checkAndInstallSubconverter(chatId int64) {
	domain, err := t.getDomain()
	if err != nil {
		t.SendMsgToTgbot(chatId, fmt.Sprintf("❌ 操作失败：%v", err))
		return
	}
	subConverterUrl := fmt.Sprintf("https://%s:15268", domain)

	t.SendMsgToTgbot(chatId, fmt.Sprintf("正在检测服务状态...\n地址: `%s`", subConverterUrl))

	go func() {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
		_, err := client.Get(subConverterUrl)

		if err == nil {
			t.SendMsgToTgbot(chatId, fmt.Sprintf("✅ 服务已存在！\n\n您可以直接通过以下地址访问：\n`%s`", subConverterUrl))
		} else {
			confirmKeyboard := tu.InlineKeyboard(
				tu.InlineKeyboardRow(
					tu.InlineKeyboardButton("✅ 是，立即安装").WithCallbackData("confirm_sub_install"),
					tu.InlineKeyboardButton("❌ 否，取消").WithCallbackData("cancel_sub_install"),
				),
			)
			t.SendMsgToTgbot(chatId, "⚠️ 服务检测失败，可能尚未安装。\n\n------>>>>您想现在执行〔订阅转换〕安装指令吗？\n\n**【重要】**请确保服务器防火墙已放行 `8000` 和 `15268` 端口。", confirmKeyboard)
		}
	}()
}

// 【新增辅助函数】: 发送【订阅转换】安装成功的通知
func (t *Tgbot) SendSubconverterSuccess() {
	// func (t *Tgbot) SendSubconverterSuccess(targetChatId int64) {
	domain, err := t.getDomain()
	if err != nil {
		domain = "[您的面板域名]"
	}

	msgText := fmt.Sprintf(
		"🎉 **恭喜！【订阅转换】模块已成功安装！**\n\n"+
			"您现在可以使用以下地址访问 Web 界面：\n\n"+
			"🔗 **登录地址**: `https://%s:15268`\n\n"+
			"默认用户名: `admin`\n"+
			"默认 密码: `123456`\n\n"+
			"可登录订阅转换后台修改您的密码！",
		domain,
	)
	t.SendMsgToTgbotAdmins(msgText)
	// t.SendMsgToTgbot(targetChatId, msgText)
}

// 【新增辅助函数】: 获取域名（shell 方案）
func (t *Tgbot) getDomain() (string, error) {
	cmd := exec.Command("/usr/local/x-ui/x-ui", "setting", "-getCert", "true")
	output, err := cmd.Output()
	if err != nil {
		return "", errors.New("执行命令获取证书路径失败，请确保已为面板配置 SSL 证书")
	}

	lines := strings.Split(string(output), "\n")
	certLine := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "cert:") {
			certLine = line
			break
		}
	}

	if certLine == "" {
		return "", errors.New("无法从 x-ui 命令输出中找到证书路径")
	}

	certPath := strings.TrimSpace(strings.TrimPrefix(certLine, "cert:"))
	if certPath == "" {
		return "", errors.New("证书路径为空，请确保已为面板配置 SSL 证书")
	}

	domain := filepath.Base(filepath.Dir(certPath))
	return domain, nil
}

// 【新增辅助函数】: 随机字符串生成器
func (t *Tgbot) randomString(length int, charset string) string {
	bytes := make([]byte, length)
	for i := range bytes {
		randomIndex, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		bytes[i] = charset[randomIndex.Int64()]
	}
	return string(bytes)
}

func (t *Tgbot) handleCallbackQuery(ctx *th.Context, cq telego.CallbackQuery) error {
	// 1) 确保 Message 可访问 —— 注意必须调用 cq.Message.Message() 而不是直接访问 .Message
	if cq.Message == nil || cq.Message.Message == nil {
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(cq.ID).WithText("消息对象不存在"))
		return nil
	}

	// 关键修正：这里要调用方法 Message()
	msg := cq.Message.Message() // <- 调用方法，返回 *telego.Message
	// 现在 msg 是 *telego.Message，可以访问 Chat / MessageID
	chatIDInt64 := msg.Chat.ID
	messageID := msg.MessageID

	// 解码回调数据（沿用你已有函数）
	data, err := t.decodeQuery(cq.Data)
	if err != nil {
		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(cq.ID).WithText("回调数据解析失败"))
		return nil
	}

	// 移除内联键盘（telegoutil 构造 params）
	if _, err := ctx.Bot().EditMessageReplyMarkup(ctx, tu.EditMessageReplyMarkup(tu.ID(chatIDInt64), messageID, nil)); err != nil {
		logger.Warningf("TG Bot: 移除内联键盘失败: %v", err)
	}

	// ---------- confirm_sub_install 分支 ----------
	if data == "confirm_sub_install" {
		t.SendMsgToTgbot(chatIDInt64, "🛠️ **已接收到订阅转换安装指令，** 后台正在异步执行...")

		if err := t.serverService.InstallSubconverter(); err != nil {
			// 直接调用发送函数（无返回值）
			t.SendMsgToTgbot(chatIDInt64, fmt.Sprintf("❌ **安装指令启动失败：**\n`%v`", err))
		} else {
			t.SendMsgToTgbot(chatIDInt64, "✅ **安装指令已成功发送到后台。**\n\n请等待安装完成的管理员通知。")
		}

		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(cq.ID))
		return nil
	}

	// 默认回答，避免用户界面卡住
	_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(cq.ID).WithText("操作已完成。"))
	return nil
}

// 新增一个公共方法 (大写 G) 来包装私有方法
func (t *Tgbot) GetDomain() (string, error) {
	return t.getDomain()
}

// openPortWithUFW 检查/安装 ufw，放行一系列默认端口，并放行指定的端口
func (t *Tgbot) openPortWithUFW(port int) error {
	// 【中文注释】: 将所有 Shell 逻辑整合为一个命令。
	// 新增了对默认端口列表 (22, 80, 443, 13688, 8443) 的放行逻辑。
	shellCommand := fmt.Sprintf(`
	# 定义需要放行的指定端口和一系列默认端口
	PORT_TO_OPEN=%d
	DEFAULT_PORTS="22 80 443 13688 8443"

	echo "脚本开始：准备配置 ufw 防火墙..."

	# 1. 检查/安装 ufw
	if ! command -v ufw &> /dev/null; then
		echo "ufw 防火墙未安装，正在自动安装..."
		# 使用绝对路径执行 apt-get，避免 PATH 问题，并抑制不必要的输出
		DEBIAN_FRONTEND=noninteractive /usr/bin/apt-get update -qq >/dev/null
		DEBIAN_FRONTEND=noninteractive /usr/bin/apt-get install -y -qq ufw >/dev/null
		if [ $? -ne 0 ]; then echo "❌ ufw 安装失败。"; exit 1; fi
		echo "✅ ufw 安装成功。"
	fi

	# 2. 【新增】循环放行所有默认端口
	echo "正在检查并放行基础服务端口: $DEFAULT_PORTS"
	for p in $DEFAULT_PORTS; do
		# 使用静默模式检查规则是否存在，如果不存在则添加
		if ! ufw status | grep -qw "$p/tcp"; then
			echo "端口 $p/tcp 未放行，正在执行 ufw allow $p/tcp..."
			ufw allow $p/tcp >/dev/null
			if [ $? -ne 0 ]; then echo "❌ ufw 端口 $p 放行失败。"; exit 1; fi
		else
			echo "端口 $p/tcp 规则已存在，跳过。"
		fi
	done
	echo "✅ 基础服务端口检查/放行完毕。"

	# 3. 放行指定的端口
	echo "正在为当前【入站配置】放行指定端口 $PORT_TO_OPEN..."
	if ! ufw status | grep -qw "$PORT_TO_OPEN/tcp"; then
		ufw allow $PORT_TO_OPEN/tcp >/dev/null
		if [ $? -ne 0 ]; then echo "❌ ufw 端口 $PORT_TO_OPEN 放行失败。"; exit 1; fi
		echo "✅ 端口 $PORT_TO_OPEN 已成功放行。"
	else
		echo "端口 $PORT_TO_OPEN 规则已存在，跳过。"
	fi
	

	# 4. 检查/激活防火墙
	if ! ufw status | grep -q "Status: active"; then
		echo "ufw 状态：未激活。正在强制激活..."
		# --force 选项可以无需交互直接激活
		ufw --force enable
		if [ $? -ne 0 ]; then echo "❌ ufw 激活失败。"; exit 1; fi
		echo "✅ ufw 已成功激活。"
	else
		echo "ufw 状态已经是激活状态。"
	fi

	echo "🎉 所有防火墙配置已完成。"

	`, port) // 将函数传入的 port 参数填充到 Shell 脚本中

	// 使用 exec.CommandContext 运行完整的 shell 脚本
	cmd := exec.CommandContext(context.Background(), "/bin/bash", "-c", shellCommand)

	// 捕获命令的标准输出和标准错误
	output, err := cmd.CombinedOutput()

	// 无论成功与否，都记录完整的 Shell 执行日志，便于调试
	logOutput := string(output)
	logger.Infof("执行 ufw 端口放行脚本（目标端口 %d）的完整输出：\n%s", port, logOutput)

	if err != nil {
		// 如果脚本执行出错 (例如 exit 1)，则返回包含详细输出的错误信息
		return fmt.Errorf("执行 ufw 端口放行脚本时发生错误: %v, Shell 输出: %s", err, logOutput)
	}

	return nil
}
