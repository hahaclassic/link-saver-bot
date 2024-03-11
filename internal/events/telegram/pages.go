package telegram

import (
	"context"
	"errors"

	tgclient "github.com/hahaclassic/golang-telegram-bot.git/internal/clients/telegram"
	"github.com/hahaclassic/golang-telegram-bot.git/internal/storage"
	"github.com/hahaclassic/golang-telegram-bot.git/lib/errhandling"
)

func (p *Processor) chooseTag(ctx context.Context, ChatID int) (err error) {

	button := []string{"without a tag"}
	replyMarkup, err := tgclient.CreateInlineKeyboardMarkup(button, button)
	if err != nil {
		return err
	}
	_, err = p.tg.SendCallbackMessage(ChatID, msgEnterUrlName, replyMarkup)
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
		p.sessions[UserID].currentOperation = DoneCmd
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

func (p *Processor) sendRandom(ctx context.Context, chatID int, userID int) (err error) {
	defer func() { err = errhandling.WrapIfErr("can't do command: can't send random", err) }()

	page, err := p.storage.PickRandom(ctx, userID)
	if err != nil && !errors.Is(err, storage.ErrNoSavedPages) {
		return err
	}
	if errors.Is(err, storage.ErrNoSavedPages) {
		return p.tg.SendMessage(chatID, msgNoSavedPages)
	}

	return p.tg.SendMessage(chatID, page)
}

func (p *Processor) chooseLinkForDeletion(ctx context.Context, ChatID int, UserID int) error {

	access, err := p.storage.AccessLevelByUserID(ctx, p.sessions[UserID].folderID, UserID)
	if err != nil {
		return err
	}
	if access != storage.Owner && access != storage.Editor {
		p.sessions[UserID].currentOperation = DoneCmd
		return p.tg.SendMessage(ChatID, msgIncorrectAccessLvl)
	}

	tags, err := p.storage.GetTags(ctx, p.sessions[UserID].folderID)
	if err != nil {
		return errhandling.Wrap("can't show folder", err)
	}

	if len(tags) == 0 {
		p.tg.SendMessage(ChatID, msgEmptyFolder)
		return ErrEmptyFolder
	}

	replyMarkup, err := tgclient.CreateInlineKeyboardMarkup(tags, tags)
	if err != nil {
		return err
	}

	messageID, err := p.tg.SendCallbackMessage(ChatID, msgChooseLink, replyMarkup)
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