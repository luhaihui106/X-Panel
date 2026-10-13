package service

// Only external host effects are replaceable. Authorization and real callback
// dispatch always run; SQLite mutations remain real in isolated acceptance.
// Production leaves this nil and retains the existing implementation.
type telegramExternalEffects struct {
	backup       func(int64)
	checkInstall func(int64)
	install      func() error
	restart      func(int64)
}

type databaseImportCoreEffects struct {
	stop    func()
	restart func() error
}
