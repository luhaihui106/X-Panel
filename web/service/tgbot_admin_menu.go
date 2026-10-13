package service

import "github.com/mymmrac/telego"

// Group only existing capabilities; ordinary users keep their own-client menu.
func (t *Tgbot) sendAdminGroup(actor int64, group string) {
	groups := map[string][][2]string{
		"menu_root":    {{"状态查询", "menu_status"}, {"客户端管理", "menu_clients"}, {"备份", "menu_backup"}, {"系统操作", "menu_system"}},
		"menu_status":  {{t.I18nBot("tgbot.buttons.serverUsage"), "get_usage"}, {t.I18nBot("tgbot.buttons.getInbounds"), "inbounds"}, {t.I18nBot("tgbot.buttons.onlines"), "onlines"}, {t.I18nBot("tgbot.buttons.SortedTrafficUsageReport"), "get_sorted_traffic_usage_report"}, {t.I18nBot("tgbot.buttons.depleteSoon"), "deplete_soon"}, {t.I18nBot("tgbot.buttons.getBanLogs"), "get_banlogs"}},
		"menu_clients": {{t.I18nBot("tgbot.buttons.allClients"), "get_inbounds"}, {t.I18nBot("tgbot.buttons.addClient"), "add_client"}, {t.I18nBot("tgbot.buttons.ResetAllTraffics"), "reset_all_traffics"}},
		"menu_backup":  {{t.I18nBot("tgbot.buttons.dbBackup"), "get_backup"}},
		"menu_system":  {{"重启面板", "restart_panel"}, {t.I18nBot("tgbot.buttons.subconverter"), "subconverter_install"}, {t.I18nBot("tgbot.buttons.commands"), "commands"}},
	}
	keyboard := &telego.InlineKeyboardMarkup{}
	for _, b := range groups[group] {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []telego.InlineKeyboardButton{{Text: b[0], CallbackData: b[1]}})
	}
	if group != "menu_root" {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []telego.InlineKeyboardButton{{Text: "返回", CallbackData: "menu_root"}})
	}
	t.SendMsgToTgbot(actor, "请选择管理操作", keyboard)
}
