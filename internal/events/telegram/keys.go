package telegram

import (
	"context"
	"fmt"

	tgclient "github.com/hahaclassic/golang-telegram-bot.git/internal/clients/telegram"
	"github.com/hahaclassic/golang-telegram-bot.git/internal/events"
	"github.com/hahaclassic/golang-telegram-bot.git/internal/storage"
	conc "github.com/hahaclassic/golang-telegram-bot.git/lib/concatenation"
	"github.com/hahaclassic/golang-telegram-bot.git/lib/errhandling"
)

// Обновлять статус сессии в вызывающей функции
// Декомпозировать
func (p *Processor) showKeys(ctx context.Context, ChatID int, UserID int) error {

	folderID := p.sessions[UserID].folderID
	owner, err := p.storage.Owner(ctx, folderID)
	if err != nil {
		return err
	}
	if UserID != owner {
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	keys, levels := []string{}, []string{}
	for lvl := storage.Editor; lvl >= storage.Reader; lvl-- {
		key, err := p.storage.GetPassword(ctx, folderID, lvl)
		if err == storage.ErrNoPasswords {
			continue
		} else if err != nil {
			return err
		}
		keys = append(keys, "<code>"+key+"</code>")
		levels = append(levels, fmt.Sprint(lvl))
	}

	var message string
	if len(keys) == 0 {
		message = "No passwords"
	} else {
		message = conc.EnumeratedJoinWithTags(keys, levels)
	}
	buttons := []string{"Create key", "Delete key", "Check users", msgBack}
	operations := []string{CreateKeyCmd.String(), DeleteKeyCmd.String(), "Check users", GoBackCmd.String()}

	replyMarkup, err := tgclient.CreateInlineKeyboardMarkup(buttons, operations)
	if err != nil {
		return err
	}
	messageID, err := p.tg.SendCallbackMessage(ChatID, message, replyMarkup)
	if err == nil {
		p.sessions[UserID].lastMessageID = messageID
	}
	return err
}

func (p *Processor) createKey(ctx context.Context, ChatID int, UserID int, accessLvl storage.AccessLevel) (err error) {

	access, err := p.storage.AccessLevelByUserID(ctx, p.sessions[UserID].folderID, UserID)
	if err != nil {
		return err
	}
	if access != storage.Owner {
		p.sessions[UserID].currentOperation = DoneCmd
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	err = p.storage.CreatePassword(ctx, p.sessions[UserID].folderID, accessLvl)
	if err != nil {
		return err
	}

	return p.tg.SendMessage(ChatID, "Ключ успешно создан.")
}

// Update sessions
func (p *Processor) deleteKey(ctx context.Context, ChatID int, UserID int, accessLvl storage.AccessLevel) (err error) {

	folderID := p.sessions[UserID].folderID
	owner, err := p.storage.Owner(ctx, folderID)
	if err != nil {
		return err
	}
	if UserID != owner {
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	err = p.storage.DeletePassword(ctx, folderID, accessLvl)
	if err == storage.ErrNoPasswords {
		return p.tg.SendMessage(ChatID, "Ключа для данного уровня доступа не существует.")
	}
	if err != nil {
		return err
	}

	return p.tg.SendMessage(ChatID, "Ключ успешно удален.")
}

// KEY|FOLDER_ID|PASSWORD
func decodeKey(key string) (folderID string, password string) {
	folderID = key[3:15]
	password = key[15:]
	return folderID, password
}

func (p *Processor) sendConfirmationMessage(ctx context.Context, prevAccessLvl storage.AccessLevel, accessData *AccessData) error {
	owner, err := p.storage.Owner(ctx, accessData.FolderID)
	if err != nil {
		return err
	}

	message := accessData.CreateMessage()
	callbackDataForYes := accessData.EncodeCallbackData()

	if prevAccessLvl == storage.Suspected {
		accessData.AccessLevel = storage.Banned
	} else {
		accessData.AccessLevel = storage.Suspected
	}
	callbackDataForNo := accessData.EncodeCallbackData()

	replyMarkup, err := tgclient.CreateInlineKeyboardMarkup([]string{"Yes", "No"},
		[]string{callbackDataForYes, callbackDataForNo})
	if err != nil {
		return err
	}

	_, err = p.tg.SendCallbackMessage(owner, message, replyMarkup) // userID соответствует chatID
	return err
}

func (p *Processor) checkKey(ctx context.Context, event *events.Event) (err error) {

	defer func() { err = errhandling.WrapIfErr("can't check key", err) }()

	folderID, password := decodeKey(event.Text) // event.Text == key

	access, err := p.storage.AccessLevelByUserID(ctx, folderID, event.UserID)
	if err != nil {
		return err
	}
	if access == storage.Owner {
		return p.tg.SendMessage(event.ChatID, "Вы являетесь владельцем этой папки.")
	}
	if access == storage.Banned {
		return p.tg.SendMessage(event.ChatID, "Доступ к этой папке заблокирован.")
	}

	folderName, err := p.storage.FolderName(ctx, folderID)
	if err != nil && err != storage.ErrNoFolders {
		return err
	}

	newAccessLevel, err := p.storage.AccessLevelByPassword(ctx, folderID, password)
	if err != nil {
		return err
	}
	if newAccessLevel == storage.Reader {
		err = p.storage.AddFolder(ctx, &storage.Folder{
			ID:        folderID,
			Name:      folderName + PublicFolderSpecSymb,
			AccessLvl: storage.Reader,
			UserID:    event.UserID,
			Username:  event.Username,
		})
		if err != nil {
			return err
		}
		return p.tg.SendMessage(event.ChatID, "Папка добавлена успешно.")
	}

	accessData := createAccessData(folderID, folderName, newAccessLevel, event.UserID, event.Username)

	return p.sendConfirmationMessage(ctx, access, accessData)
}

func (p *Processor) setAccess(ctx context.Context, ownerChatID int, callbackData string, message string) (err error) {

	defer func() { errhandling.WrapIfErr("can't set access", err) }()

	accessData, err := decodeAccessData(callbackData, message)
	if err != nil {
		return err
	}

	err = p.storage.DeleteAccess(ctx, accessData.UserID, accessData.FolderID)
	if err != nil && err != storage.ErrNoRows {
		return err
	}

	// AddFolder будет иметь другие параметры после реструктуризации бд и разделении таблиц
	err = p.storage.AddFolder(ctx, &storage.Folder{
		ID:        accessData.FolderID,
		Name:      accessData.FolderName + PublicFolderSpecSymb,
		AccessLvl: accessData.AccessLevel,
		UserID:    accessData.UserID,
		Username:  accessData.Username,
	})
	if err != nil {
		return err
	}

	return p.SendResultOfGaingAccess(ownerChatID, accessData)
}

// переименовать получше
// Добавить обработку ошибок
func (p *Processor) SendResultOfGaingAccess(ownerChatID int, accessData *AccessData) (err error) {
	switch accessData.AccessLevel {
	case storage.Suspected:
		_ = p.tg.SendMessage(ownerChatID, `При следующем отказе пользователь
		 будет заблокирован, и вы больше не будете получать от него уведомления насчет этой папки.`)
		_ = p.tg.SendMessage(accessData.UserID, `Вам отказано в доступе.`)
	case storage.Banned:
		_ = p.tg.SendMessage(ownerChatID, `Пользователь заблокирован.`)
		_ = p.tg.SendMessage(accessData.UserID, `Вам отказано в доступе.`)
	default:
		_ = p.tg.SendMessage(ownerChatID, fmt.Sprintf("Пользователь '%s' получил доступ к папке '%s'.",
			accessData.Username, accessData.FolderName))
		_ = p.tg.SendMessage(accessData.UserID, fmt.Sprintf("Вы получили доступ к папке '%s'.",
			accessData.FolderName))
	}

	return err
}

func (p *Processor) chooseAccessLvl(ChatID int, UserID int, operation Operation) error {

	names := []string{}
	data := []string{}
	for lvl := storage.Editor; lvl >= storage.Reader; lvl-- {
		names = append(names, lvl.String())
		data = append(data, operation.String()+","+lvl.String())
		fmt.Println(names)
	}
	names = append(names, msgBack)
	data = append(data, GoBackCmd.String())

	replyMarkup, err := tgclient.CreateInlineKeyboardMarkup(names, data)
	if err != nil {
		return err
	}

	messageID, err := p.tg.SendCallbackMessage(ChatID, "Choose access level", replyMarkup)
	if err == nil {
		p.sessions[UserID].lastMessageID = messageID
	}
	return err
}