package faceit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
	"reflect"

	"github.com/bwmarrin/discordgo"
	"github.com/goodsign/monday"
)

const DiscordChannelID = "734913712737091594"

func (f *FaceitWebhook) StartBot() error {
	discord, err := discordgo.New(f.Token)
	if err != nil {
		return err
	}

	if err := discord.Open(); err != nil {
		return err
	}

	f.discord = discord

	return nil
}

func (f *FaceitWebhook) SendDiscordAlert(e Event) error {

	msg, err := getDiscordMessage(e)
	if err != nil {
		return err
	}

	_, err = f.discord.ChannelMessageSendEmbed(DiscordChannelID, msg)
	return err
}

func getDiscordMessage(e Event) (*discordgo.MessageEmbed, error) {

	_, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}

	re := reflect.ValueOf(e).Elem()
	now := time.Now()
	fields := []*discordgo.MessageEmbedField{}
	
	for i := 0; i < re.NumField(); i++ {

		val := re.Field(i).Interface()
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: re.Type().Field(i).Name,
		})

		switch re.Type().Field(i).Type {
		case reflect.TypeOf(now):
			fields[i].Value = monday.Format(val.(time.Time), "2006-01-02 15:04:05", monday.LocaleEnUS)
		case reflect.TypeOf(MatchStatusCancelled), reflect.TypeOf(""):
			fields[i].Value = fmt.Sprintf("%s", val)
		case reflect.TypeOf(1):
			fields[i].Value = fmt.Sprintf("%d", val)
		case reflect.TypeOf(struct{}{}):

			data, err := json.Marshal(val)
			if err != nil {
				return nil, err
			}

			var prettyJSON bytes.Buffer
			if err := json.Indent(&prettyJSON, data, "", "\t"); err != nil {
				return nil, err
			}

			fields[i].Value = string(prettyJSON.Bytes())
		}
	}

	for _,f := range fields {
		fmt.Printf("%v\n", f)
	}

	return &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			IconURL: "https://cdn.discordapp.com/avatars/701226924063588402/aadd4d6d59ef2466096cdbe004c3c367.png?size=256",
			Name:    "💃 Naᖙia   🎉",
			URL:     "https://www.faceit.com/en/players/BOT_PPL",
		},
		Color:       0x00ff00, // Green
		Description: e.String(),
		Fields:      fields,
		Timestamp:   time.Now().Format(time.RFC3339), // Discord wants ISO8601; RFC3339 is an extension of ISO8601 and should be completely compatible.
		Title:       fmt.Sprintf("NEW Event: %s", e.String()),
	}, nil
}