package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/hahaclassic/golang-telegram-bot.git/events"
	conc "github.com/hahaclassic/golang-telegram-bot.git/lib/concatenation"
	"github.com/hahaclassic/golang-telegram-bot.git/lib/errhandling"
	"github.com/hahaclassic/golang-telegram-bot.git/storage"
)

func (p *Processor) doCallbackCmd(event *events.Event, meta *CallbackMeta) (err error) {

	defer func() {
		_ = p.tg.AnswerCallbackQuery(meta.QueryID)
		if err == ErrEmptyFolder {
			err = nil
		}
		err = errhandling.WrapIfErr("can't do callback cmd", err)
	}()

	if len(meta.Data) > 7 && meta.Data[:7] == GetAccessCmd {
		return p.setAccess(context.Background(), event.ChatID, meta.Data, event.Text)
	}

	err = p.tg.DeleteMessage(event.ChatID, p.sessions[event.UserID].lastMessageID)
	if err != nil {
		return err
	}

	if meta.Data == GoBackCmd {
		return p.goBack(context.Background(), event)
	}

	if meta.Data == CreateKeyCmd || meta.Data == DeleteKeyCmd {
		p.sessions[event.UserID].currentOperation = meta.Data
	} else if storage.ToAccessLvl(meta.Data) != storage.Undefined {
		log.Println("YES")
		p.sessions[event.UserID].status = statusOK
		if p.sessions[event.UserID].currentOperation == CreateKeyCmd {
			return p.createKey(context.Background(), event.ChatID, event.UserID, storage.ToAccessLvl(meta.Data))
		} else {
			return p.deleteKey(context.Background(), event.ChatID, event.UserID, storage.ToAccessLvl(meta.Data))
		}
	} else if p.sessions[event.UserID].currentOperation == DeleteLinkCmd {
		p.sessions[event.UserID].tag = meta.Data
	} else {
		p.sessions[event.UserID].folderID = meta.Data
	}

	switch p.sessions[event.UserID].currentOperation {

	case ChooseFolderForRenamingCmd:
		p.sessions[event.UserID].currentOperation = RenameFolderCmd
		return p.tg.SendMessage(event.ChatID, msgEnterNewFolderName)

	case ChooseLinkForDeletionCmd:
		p.sessions[event.UserID].currentOperation = DeleteLinkCmd
		return p.chooseLinkForDeletion(context.Background(), event.ChatID, event.UserID)

	case GetNameCmd:
		p.sessions[event.UserID].currentOperation = SaveLinkCmd
		p.sessions[event.UserID].tag = p.sessions[event.UserID].url
		err = p.chooseFolder(context.Background(), event.ChatID, event.UserID)

	case KeyCmd:
		return p.showKeys(context.Background(), event.ChatID, event.UserID)

	case CreateKeyCmd:
		return p.chooseAccessLvl(event.ChatID, event.UserID)

	case DeleteKeyCmd:
		return p.chooseAccessLvl(event.ChatID, event.UserID)

	case SaveLinkCmd:
		p.sessions[event.UserID].status = statusOK
		return p.savePage(context.Background(), event.ChatID, event.UserID)

	case ShowFolderCmd:
		p.sessions[event.UserID].status = statusOK
		return p.showFolder(context.Background(), event.ChatID, event.UserID)

	case DeleteFolderCmd:
		p.sessions[event.UserID].status = statusOK
		return p.deleteFolder(context.Background(), event.ChatID, event.UserID)

	case DeleteLinkCmd:
		p.sessions[event.UserID].status = statusOK
		return p.deleteLink(context.Background(), event.ChatID, event.UserID)
	}

	return nil
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

func (p *Processor) goBack(ctx context.Context, event *events.Event) (err error) {
	switch p.sessions[event.UserID].currentOperation {
	case CreateKeyCmd:
		p.sessions[event.UserID].currentOperation = KeyCmd
		err = p.showKeys(ctx, event.ChatID, event.UserID)
	case DeleteKeyCmd:
		p.sessions[event.UserID].currentOperation = KeyCmd
		err = p.showKeys(ctx, event.ChatID, event.UserID)
	case KeyCmd:
		p.sessions[event.UserID].currentOperation = KeyCmd
		err = p.chooseFolder(ctx, event.ChatID, event.UserID)
	}

	return err
}

func (p *Processor) savePage(ctx context.Context, ChatID int, UserID int) (err error) {
	defer func() { err = errhandling.WrapIfErr("can't save page", err) }()

	session := p.sessions[UserID]

	access, err := p.storage.AccessLevelByUserID(ctx, session.folderID, UserID)
	if err != nil {
		return err
	}
	if access != storage.Owner && access != storage.Editor {
		p.sessions[UserID].status = statusOK
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	page := p.storage.NewPage(session.url, session.tag, session.folderID)

	isExists, err := p.storage.IsPageExist(ctx, page)
	if err != nil {
		return err
	}
	if isExists {
		return p.tg.SendMessage(ChatID, msgAlreadyExists)
	}

	if err := p.storage.SavePage(ctx, page); err != nil {
		return err
	}

	return p.tg.SendMessage(ChatID, msgSaved)
}

func (p *Processor) showFolder(ctx context.Context, ChatID int, UserID int) (err error) {

	defer func() { err = errhandling.WrapIfErr("can't show folder", err) }()

	session := p.sessions[UserID]
	urls, err := p.storage.GetLinks(ctx, session.folderID)
	if err != nil {
		return err
	}

	tags, err := p.storage.GetTags(ctx, session.folderID)
	if err != nil {
		return errhandling.Wrap("can't show folder", err)
	}
	if len(urls) == 0 {
		return p.tg.SendMessage(ChatID, msgEmptyFolder)
	}

	folderName, err := p.storage.FolderName(ctx, session.folderID)
	if err != nil {
		return err
	}
	result := folderName + ":\n\n" + conc.EnumeratedJoinWithTags(urls, tags)

	return p.tg.SendMessage(ChatID, result)
}

func (p *Processor) deleteFolder(ctx context.Context, ChatID int, UserID int) error {

	access, err := p.storage.AccessLevelByUserID(ctx, p.sessions[UserID].folderID, UserID)
	if err != nil {
		return err
	}

	if access == storage.Owner {
		err = p.storage.RemoveFolder(ctx, p.sessions[UserID].folderID)
	} else {
		err = p.storage.DeleteAccess(ctx, UserID, p.sessions[UserID].folderID)
	}

	return p.tg.SendMessage(ChatID, msgFolderDeleted)
}

func (p *Processor) chooseLinkForDeletion(ctx context.Context, ChatID int, UserID int) error {

	access, err := p.storage.AccessLevelByUserID(ctx, p.sessions[UserID].folderID, UserID)
	if err != nil {
		return err
	}
	if access != storage.Owner && access != storage.Editor {
		p.sessions[UserID].status = statusOK
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	urls, err := p.storage.GetTags(ctx, p.sessions[UserID].folderID)
	if err != nil {
		return errhandling.Wrap("can't show folder", err)
	}

	if len(urls) == 0 {
		p.tg.SendMessage(ChatID, msgEmptyFolder)
		return ErrEmptyFolder
	}

	messageID, err := p.tg.SendCallbackMessage(ChatID, msgChooseLink, urls, urls)
	if err == nil {
		p.sessions[UserID].lastMessageID = messageID
	}

	return err
}

func (p *Processor) deleteLink(ctx context.Context, ChatID int, UserID int) (err error) {

	session := p.sessions[UserID]
	// Т.к. поле name является уникальным в отдельной папке, то удаление происходит по нему
	// и URL в следующей строке не имеет значения.
	page := p.storage.NewPage("", session.tag, session.folderID)
	if page == nil {
		return errors.New("can't delete link: can't create folder")
	}

	err = p.storage.RemovePage(ctx, page)
	if err != nil {
		return err
	}

	return p.tg.SendMessage(ChatID, msgPageDeleted)
}

func (p *Processor) showKeys(ctx context.Context, ChatID int, UserID int) error {

	folderID := p.sessions[UserID].folderID
	access, err := p.storage.AccessLevelByUserID(ctx, folderID, UserID)
	if err != nil {
		return err
	}
	if access != storage.Owner {
		p.sessions[UserID].status = statusOK
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	keys := []string{}
	names := []string{}
	for lvl := storage.Editor; lvl >= storage.Reader; lvl-- {
		key, err := p.storage.GetPassword(ctx, folderID, lvl)
		if err == storage.ErrNoPasswords {
			continue
		} else if err != nil {
			return err
		}
		keys = append(keys, "<code>"+key+"</code>")
		names = append(names, fmt.Sprint(lvl))
	}

	var message string
	if len(keys) == 0 {
		message = "No passwords"
	} else {
		message = conc.EnumeratedJoinWithTags(keys, names)
	}
	buttons := []string{"Create key", "Delete key", "Check users", msgBack}
	operations := []string{CreateKeyCmd, DeleteKeyCmd, "Check users", GoBackCmd}

	messageID, err := p.tg.SendCallbackMessage(ChatID, message, buttons, operations)
	if err == nil {
		p.sessions[UserID].lastMessageID = messageID
	}
	return err
}

func (p *Processor) chooseAccessLvl(ChatID int, UserID int) error {

	names := []string{}
	data := []string{}
	for lvl := storage.Editor; lvl >= storage.Reader; lvl-- {
		names = append(names, fmt.Sprint(lvl))
		data = append(data, fmt.Sprint(lvl))
		fmt.Println(names)
	}
	names = append(names, msgBack)
	data = append(data, GoBackCmd)

	messageID, err := p.tg.SendCallbackMessage(ChatID, "Choose access level", names, data)
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
		p.sessions[UserID].status = statusOK
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	err = p.storage.CreatePassword(ctx, p.sessions[UserID].folderID, accessLvl)
	if err != nil {
		return err
	}

	return p.tg.SendMessage(ChatID, "Ключ успешно создан.")
}

func (p *Processor) deleteKey(ctx context.Context, ChatID int, UserID int, accessLvl storage.AccessLevel) (err error) {

	access, err := p.storage.AccessLevelByUserID(ctx, p.sessions[UserID].folderID, UserID)
	if err != nil {
		return err
	}
	if access != storage.Owner {
		p.sessions[UserID].status = statusOK
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	err = p.storage.DeletePassword(ctx, p.sessions[UserID].folderID, accessLvl)
	if err == storage.ErrNoPasswords {
		return p.tg.SendMessage(ChatID, "Ключа для данного уровня доступа не существует.")
	}
	if err != nil {
		return err
	}

	return p.tg.SendMessage(ChatID, "Ключ успешно удален.")
}
