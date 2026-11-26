package main

import (
	"context"
	"errors"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type feishuBotGroup struct {
	ChatID      string `json:"chat_id"`
	DisplayName string `json:"display_name"`
}

func listBotGroups(ctx context.Context, client *lark.Client) ([]feishuBotGroup, error) {
	groups := []feishuBotGroup{}
	pageToken := ""
	seenTokens := map[string]bool{}
	for {
		builder := larkim.NewListChatReqBuilder().Types("group").PageSize(100)
		if pageToken != "" {
			builder.PageToken(pageToken)
		}
		// The client holds app credentials and uses a tenant token, so this
		// endpoint lists the bot's groups instead of the owner's groups.
		response, err := client.Im.V1.Chat.List(ctx, builder.Build())
		if err != nil {
			return nil, err
		}
		if !response.Success() || response.Data == nil {
			return nil, fmt.Errorf("Feishu group list rejected: %d", response.Code)
		}
		for _, chat := range response.Data.Items {
			if chat == nil || optionalString(chat.ChatId) == "" || optionalString(chat.ChatMode) == "p2p" {
				continue
			}
			groups = append(groups, feishuBotGroup{ChatID: optionalString(chat.ChatId), DisplayName: optionalString(chat.Name)})
		}
		if response.Data.HasMore == nil || !*response.Data.HasMore {
			return groups, nil
		}
		pageToken = optionalString(response.Data.PageToken)
		if pageToken == "" || seenTokens[pageToken] {
			return nil, errors.New("invalid Feishu group pagination")
		}
		seenTokens[pageToken] = true
	}
}
