package fc

import (
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/goodsign/monday"
)

const DiscordChannelID = "734913712737091594"

var (
	MatchStatusColors map[EventName]int = map[EventName]int{
		MatchObjectCreated:     0xa6a6a6,
		MatchStatusAborted:     0xcc3300,
		MatchStatusCancelled:   0xcc3300,
		MatchStatusConfiguring: 0xffcc00,
		MatchStatusFinished:    0x1D6300,
		MatchStatusReady:       0x9AFB9A,
	}
)

func (f *FaceitWebhook) StartBot() error {

	log.Printf("[DISCORD] bot token: %s", f.Token)

	discord, err := discordgo.New(fmt.Sprintf("Bot %s", f.Token))
	if err != nil {
		return err
	}

	if err := discord.Open(); err != nil {
		return err
	}

	f.discord = discord

	return nil
}

func (f *FaceitWebhook) SendDiscordAlert(t *Transaction) error {

	msg, err := f.getDiscordMessage(t)
	if err != nil {
		return err
	}

	_, err = f.discord.ChannelMessageSendEmbed(DiscordChannelID, msg)
	return err
}

func (f *FaceitWebhook) getDiscordMessage(t *Transaction) (*discordgo.MessageEmbed, error) {

	e := t.Payload
	fields := []*discordgo.MessageEmbedField{}
	now := time.Now()
	re := reflect.ValueOf(e).Elem()

	for i := 0; i < re.NumField(); i++ {

		// val := re.Field(i).Interface()

		// fields = append(fields, &discordgo.MessageEmbedField{
		// 	Name: re.Type().Field(i).Name,
		// })

		// switch re.Type().Field(i).Type {
		// case reflect.TypeOf(now):
		// 	fields[i].Value = monday.Format(val.(time.Time), "2006-01-02 15:04:05", monday.LocaleEnUS)
		// case reflect.TypeOf(MatchStatusCancelled), reflect.TypeOf(""):
		// 	fields[i].Value = fmt.Sprintf("%s", val)
		// case reflect.TypeOf(1):
		// 	fields[i].Value = fmt.Sprintf("%d", val)
		// case reflect.TypeOf(struct{}{}):

		// 	data, err := json.Marshal(val)
		// 	if err != nil {
		// 		return nil, err
		// 	}

		// 	var prettyJSON bytes.Buffer
		// 	if err := json.Indent(&prettyJSON, data, "", "\t"); err != nil {
		// 		return nil, err
		// 	}

		// 	fields[i].Value = string(prettyJSON.Bytes())
		// }
	}

	for _, f := range fields {
		fmt.Printf("%v\n", f)
	}

	msg := &discordgo.MessageEmbed{
		Author: &discordgo.MessageEmbedAuthor{
			IconURL: "https://cdn.discordapp.com/avatars/701226924063588402/aadd4d6d59ef2466096cdbe004c3c367.png?size=256",
			Name:    "💃 Naᖙia   🎉",
			URL:     "https://www.faceit.com/en/players/BOT_PPL",
		},
		Description: fmt.Sprintf("[%s](%s)", e.String(), e.URL()),
		Color:       e.Color(), // Green
		Fields:      fields,
		// Timestamp:   time.Now().Format("2006-01-02 15:04:05"), // Discord wants ISO8601; RFC3339 is an extension of ISO8601 and should be completely compatible.
		Title: e.String(),
		URL:   e.URL(),
		Footer: &discordgo.MessageEmbedFooter{
			Text:    fmt.Sprintf("%s | %s", ver(), monday.Format(now, "2006-01-02 15:04:05", monday.LocalePlPL)),
			IconURL: e.IconURL(),
		},
		Type: t.Name.String(),
	}

	if err := f.updateMessage(msg, t); err != nil {
		return nil, err
	}

	return msg, nil
}

func getType(myvar interface{}) string {
	if t := reflect.TypeOf(myvar); t.Kind() == reflect.Ptr {
		return "*" + t.Elem().Name()
	} else {
		return t.Name()
	}
}

func ver() string {
	bn, _ := strconv.Atoi(os.Getenv("NAD_BUILD_NUM"))
	bn += 1000
	return fmt.Sprintf("%s, Build: %d (%s)", os.Getenv("NAD_BUILD_VER"), bn, os.Getenv("NAD_BUILD_TIMESTAMP"))
}

func (f *FaceitWebhook) updateMessage(msg *discordgo.MessageEmbed, t *Transaction) error {
	switch t.Payload.(type) {
	case *Match:
		match := t.Payload.(*Match)
		msg.Fields = append(msg.Fields, &discordgo.MessageEmbedField{
			Name:  "Hub",
			Value: fmt.Sprintf("[%s](%s)", match.Hub.Name, match.Hub.URL()),
		})

		msg.Description = fmt.Sprintf("%s", match.StatusTranslated())

		if match.Status == MatchStatusCancelled && match.ClientCustom != nil {
			f.LogMsg("updating matchV2: %s", match.String())

			if err := f.updateMatch(match); err != nil {
				return err
			}
		}

		if match.Status == MatchStatusFinished && match.Detections != nil && match.Detections.Leavers == true {
			for _, pid := range match.Results[0].Leavers {
				leaver := &Player{ID: pid}
				if err := f.updatePlayer(leaver); err != nil {
					return fmt.Errorf("could not get leaver update player response, err: %v", err)
				}

				msg.Fields = append(msg.Fields, &discordgo.MessageEmbedField{
					Name:   "Znikacz",
					Value:  fmt.Sprintf("[%s](%s)", leaver.Nickname, leaver.URL()),
					Inline: false,
				})
			}
		}

		if match.Status == MatchStatusCancelled &&
			len(match.Results) > 0 &&
			(match.Detections.Leavers == true || match.Detections.Afk == true) {

			uids := []string{}
			uids = append(uids, match.Results[0].Leavers...)
			uids = append(uids, match.Results[0].AFK...)

			for _, pid := range match.Results[0].Leavers {
				afker := &Player{ID: pid}
				if err := f.updatePlayer(afker); err != nil {
					return fmt.Errorf("could not get leaver update player response, err: %v", err)
				}

				msg.Fields = append(msg.Fields, &discordgo.MessageEmbedField{
					Name:   "AFK",
					Value:  fmt.Sprintf("[%s](%s)", afker.Nickname, afker.URL()),
					Inline: false,
				})
			}
		}

		if match.ClientCustom != nil {
			sb := match.ScoreBoard()
			scores := []int{sb.Team1, sb.Team2}
			for i, t := range match.Teams {
				msg.Fields = append(msg.Fields, &discordgo.MessageEmbedField{
					Name:   fmt.Sprintf("Wynik: %s", t.Name),
					Value:  fmt.Sprintf("*%d*", scores[i]),
					Inline: true,
				})
			}
		}

	case *Hub:
		// case HubInvitation:
		// case Role:
		// case Tournament:
	}

	return nil
}
