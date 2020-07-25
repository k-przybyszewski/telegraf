package fc

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"time"

	"bitbucket.org/nadia/faceit-client"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/metric"
	"github.com/thoas/go-funk"
)

const (
	meas         = "faceit_webhooks"
	DefaultColor = 0xf5f5ef
	PPLOrganizer = "8bfed0d9-1fdf-4182-931a-6cb63571d3c6"
)

type EventName string

func (en EventName) String() string {
	return string(en)
}

type Event interface {
	NewMetric() telegraf.Metric
	String() string
	URL() string
	IconURL() string
	Color() int
}

const (
	HubCreated EventName = EventName("hub_created")
	HubUpdated EventName = EventName("hub_updated")

	HubRoleCreated EventName = EventName("hub_role_created")
	HubRoleDeleted EventName = EventName("hub_role_deleted")
	HubRoleUpdated EventName = EventName("hub_role_updated")

	HubUserInvited EventName = EventName("hub_user_invited")
	HubUserAdded   EventName = EventName("hub_user_added")
	HubUserRemoved EventName = EventName("hub_user_removed")

	HubUserRoleAdded   EventName = EventName("hub_user_role_added")
	HubUserRoleRemoved EventName = EventName("hub_user_role_removed")

	MatchObjectCreated     EventName = EventName("match_object_created")
	MatchStatusAborted     EventName = EventName("match_status_aborted")
	MatchStatusCancelled   EventName = EventName("match_status_cancelled")
	MatchStatusConfiguring EventName = EventName("match_status_configuring")
	MatchStatusFinished    EventName = EventName("match_status_finished")
	MatchStatusReady       EventName = EventName("match_status_ready")
	MatchStatusOngoing     EventName = EventName("match_status_ongoing")

	TournamentCreated   EventName = EventName("tournament_object_created")
	TournamentRemoved   EventName = EventName("tournament_object_removed")
	TournamentUpdated   EventName = EventName("tournament_object_updated")
	TournamentCancelled EventName = EventName("tournament_status_cancelled")
	TournamentCheckin   EventName = EventName("tournament_status_checkin")
	TournamentFinished  EventName = EventName("tournament_status_finished")
	TournamentSeeding   EventName = EventName("tournament_status_seeding")
	TournamentStarted   EventName = EventName("tournament_status_started")
)

var (
	finishedWarmups chan *Match          = make(chan *Match)
	statuses        map[EventName]string = map[EventName]string{
		HubCreated:             "Hub został utworzony",
		HubUpdated:             "Zaktualizowano dane hubu",
		HubRoleCreated:         "Dodano nową rolę: %s",
		HubRoleDeleted:         "Dodano nową rolę: %s",
		HubRoleUpdated:         "Dodano nową rolę: %s",
		HubUserInvited:         "Zaproszenie do: %s zostało wysłane przez: %s",
		HubUserAdded:           "Gracz: %s został dodany",
		HubUserRemoved:         "Gracz: %s został usunięty",
		HubUserRoleAdded:       "Gracz: %s dodany do roli: %s",
		HubUserRoleRemoved:     "Gracz: %s wyrzucony z grupy członków roli: %s",
		MatchObjectCreated:     "Sprawdzanie gotowości graczy",
		MatchStatusAborted:     "Odrzucony",
		MatchStatusCancelled:   "Anulowany",
		MatchStatusConfiguring: "Przygotowywanie serwera",
		MatchStatusReady:       "Gotowy",
		MatchStatusOngoing:     "Trwa",
		MatchStatusFinished: "Zakończony	",
		TournamentCreated:   "Turniej: %s - Utworzony",
		TournamentRemoved:   "Turniej: %s - Usunięty",
		TournamentUpdated:   "Turniej: %s - Zaktualizowany",
		TournamentCancelled: "Turniej: %s - Anulowany",
		TournamentCheckin:   "Turniej: %s - Gotowść",
		TournamentFinished:  "Turniej: %s - Zakończony",
		TournamentSeeding:   "Turniej: %s - Seeding",
		TournamentStarted:   "Turniej: %s - Rozpoczęty",
	}
)

type Transaction struct {
	ID           string    `json:"transaction_id"`
	Name         EventName `json:"event"`
	EventID      string    `json:"event_id"`
	ThirdPartyID string    `json:"third_party_id"`
	AppID        string    `json:"app_id"`
	Timestamp    time.Time `json:"timestamp"`
	RetryCount   int       `json:"retry_count"`
	Version      int       `json:"version"`
	Payload      Event     `json:"payload"`
}

func (t Transaction) String() string {
	return statuses[t.Name]
}

func (t Transaction) Color() int {
	return DefaultColor
}

type MatchHub struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type Match struct {
	ID          string `json:"id"`
	OrganizerID string `json:"organizer_id"`
	Region      string `json:"region"`
	Game        string `json:"game"`
	Version     int    `json:"version"`
	Hub         *Hub   `json:"entity"`
	Teams       []struct {
		ID            string      `json:"id"`
		Name          string      `json:"name"`
		Type          string      `json:"type"`
		Avatar        string      `json:"avatar"`
		LeaderID      string      `json:"leader_id"`
		CoLeaderID    string      `json:"co_leader_id"`
		Roster        []*Player   `json:"roster"`
		Substitutions int         `json:"substitutions"`
		Substitutes   interface{} `json:"substitutes"`
	} `json:"teams"`
	Status       EventName                 `json:"-"`
	CreatedAt    time.Time                 `json:"created_at"`
	UpdatedAt    time.Time                 `json:"updated_at"`
	StartedAt    time.Time                 `json:"started_at,omitempty"`
	ConfiguredAt time.Time                 `json:"configured_at,omitempty"`
	FinishedAt   time.Time                 `json:"finished_at,omitempty"`
	ClientCustom *faceit.ClientCustom      `json:"-"`
	LiveStats    *faceit.CommandResponse   `json:"-"`
	Detections   *faceit.MatchV2Detections `json:"-"`
	Results      []*faceit.MatchV2Results  `json:"-"`
	warmupDoneCh chan bool                 `json:"-"`
}

func (f *FaceitWebhook) NewMatch(en EventName) *Match {
	m := &Match{Status: en, warmupDoneCh: make(chan bool)}

	return m
}

func (mt Match) Update(f *FaceitWebhook) {

	if mt.OrganizerID != PPLOrganizer {
		return
	}

	ticker := time.NewTicker(time.Second)
	go func(match *Match) {
		for {
			now := time.Now()
			select {
			case <-match.warmupDoneCh:
				match.Status = MatchStatusOngoing
				ticker = time.NewTicker(time.Second * 30)
				fmt.Println("warmup has ended")
				// match.Update(f)
			case <-ticker.C:
				since := now.Sub(mt.ConfiguredAt)

				log.Printf("warmup since: %v", since)
				if (since < match.WarmupDuration() && match.Status == MatchStatusReady) || match.Status != MatchStatusOngoing {
					continue
				}

				match.warmupDoneCh <- true
			}
		}
	}(&mt)
}

func (mt Match) SelfV2Update(f *FaceitWebhook) error {

	log.Printf("[%v] self v2 updating...", mt)

	cacheTTL := time.Duration(time.Minute * 2)
	m, err := f.api.GetMatchV2(mt.ID, context.Background(), &cacheTTL)
	if err != nil {
		return fmt.Errorf("could not update v2 match int SelfUpdate, err: %v", err)
	}

	mt.Detections = &m.MatchCustom.Overview.Detections
	mt.Results = m.Results

	if mt.OrganizerID != PPLOrganizer {

		f.LogMsg("skipping, organizer not in intrest %s", mt.OrganizerID)

		return nil
	}

	mt.ClientCustom = m.ClientCustom

	return nil
}

func (mt Match) Color() int {
	return MatchStatusColors[mt.Status]
}

func (mt Match) ActionsURL() string {
	return fmt.Sprintf("%s/match/v2/match/%s/command", faceit.APIBaseURL)
}

func (mt Match) WarmupDuration() time.Duration {
	return time.Duration(time.Minute * 5)
}

func (mt Match) StatusTranslated() string {
	ess := map[EventName]string{
		MatchObjectCreated:     "⚫",
		MatchStatusConfiguring: "🟡",
		MatchStatusReady:       "🟢",
		MatchStatusOngoing:     "🟠",
		MatchStatusCancelled:   "🔴",
		MatchStatusFinished:    "💥",
	}

	if emoji, ok := ess[mt.Status]; ok {
		return fmt.Sprintf("%s %s", emoji, statuses[mt.Status])
	}

	return fmt.Sprintf("❓   %s", mt.ID)
}

func (mt Match) String() string {

	if len(mt.Teams) == 0 {
		return fmt.Sprintf("%s", statuses[mt.Status])
	}

	return fmt.Sprintf("%s • 𝓥𝓢 • %s", mt.Teams[0].Name, mt.Teams[1].Name)
}

func (mt Match) IsStatus(s ...EventName) bool {
	// for _, status := range s {
	// 	if status == mt.Status {
	// 		return true
	// 	}
	// }
	return funk.Contains(s, mt.Status)
}

func (mt Match) ScoreBoard() *faceit.MatchV2ScoreBoard {
	mm := faceit.MatchV2{ClientCustom: mt.ClientCustom}
	return mm.ScoreBoard()
}

func (mt Match) URL() string     { return fmt.Sprintf("https://faceit.com/pl/csgo/room/%s", mt.ID) }
func (mt Match) IconURL() string { return mt.Hub.AvatarURL }

func (mt Match) NewMetric() telegraf.Metric {

	// event := "commit_comment"

	f := map[string]interface{}{}
	t := map[string]string{}

	m, err := metric.New(meas, t, f, time.Now())
	if err != nil {
		log.Fatalf("Failed to create %v event", mt)
	}
	return m
}

type Player struct {
	ID                string                  `json:"id"`
	Nickname          string                  `json:"nickname"`
	Avatar            string                  `json:"avatar"`
	GameID            string                  `json:"game_id"`
	GameName          string                  `json:"game_name"`
	GameSkillLevel    int                     `json:"game_skill_level"`
	Membership        string                  `json:"membership"`
	AnticheatRequired bool                    `json:"anticheat_required"`
	GameStats         *faceit.PlayerGameStats `json:"stats,omitempty"`
	Game              *faceit.PlayerGame      `json:"game,omitempty"`
}

func (p *Player) String() string {
	return fmt.Sprintf("%s, %s lvl, %s elo", p.Nickname, p.LVL(), p.ELO())
}

func (p *Player) ELO() string {
	if p.Game == nil {
		return "N/A"
	}
	return fmt.Sprintf("%d", p.Game.FaceitElo)
}

func (p *Player) LVL() string {
	if p.Game == nil {
		return "N/A"
	}
	return fmt.Sprintf("%s", p.Game.SkillLevelLabel)
}

func (p *Player) MatchesCount() string {
	if p.GameStats == nil {
		return "N/A"
	}
	return fmt.Sprintf("%d", p.GameStats.Lifetime.Matches)
}

func (p *Player) URL() string {
	return fmt.Sprintf("https://faceit.com/pl/players/%s", p.ID)
}

type Hub struct {
	ID            string    `json:"id"`
	S             EventName `json:"-"`
	OrganizerID   string    `json:"organizer_id"`
	AvatarURL     string    `json:"avatar_url,omitempty"`
	PlayersJoined int       `json:"players_joined,omitempty"`
	ChatRoomID    string    `json:"chat_room_id,omitempty"`
	Name          string    `json:"name"`
	Roles         []*Role   `json:"roles,omitempty"`
}

func NewHub(s EventName) *Hub {
	return &Hub{S: s}
}

func (h Hub) Color() int {
	return DefaultColor
}

func (h Hub) String() string {
	return fmt.Sprintf(statuses[h.S], h.Name)
}

func (h Hub) URL() string {
	return fmt.Sprintf("https://faceit.com/pl/hub/%s/%s", h.ID, url.QueryEscape(h.Name))
}

func (h Hub) IconURL() string { return h.AvatarURL }

func (h Hub) NewMetric() telegraf.Metric {

	f := map[string]interface{}{}
	t := map[string]string{}

	m, err := metric.New(meas, t, f, time.Now())
	if err != nil {
		log.Fatalf("Failed to create %v event", h)
	}
	return m
}

type HubInvitation struct {
	ID          string    `json:"id"`
	S           EventName `json:"-"`
	OrganizerID string    `json:"organizer_id"`
	Type        string    `json:"type"`
	FromPlayer  *Player   `json:"-"`
	FromUser    struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	} `json:"from_user"`
	ToPlayer *Player `json:"-"`
	ToUser   struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	} `json:"to_user"`
}

func NewHubInvitation(s EventName) *HubInvitation {
	return &HubInvitation{S: s}
}

func (hi HubInvitation) Color() int {
	return DefaultColor
}

func (hi HubInvitation) IconURL() string { return "" }

func (hi HubInvitation) String() string {

	fromStr := hi.FromUser.Nickname
	toStr := fmt.Sprintf("%s [ID: %s]", hi.ToUser.Nickname, hi.ToUser.ID)

	if hi.ToPlayer != nil {
		toStr = hi.ToPlayer.String()
	}

	return fmt.Sprintf(statuses[hi.S], toStr, fromStr)
}

func (hi HubInvitation) URL() string {
	return fmt.Sprintf("FROM: https://faceit.com/pl/players/%s, TO: https://faceit.com/pl/players/%s", hi.FromUser.ID, hi.ToUser.ID)
}

func (hi HubInvitation) NewMetric() telegraf.Metric {

	f := map[string]interface{}{}
	t := map[string]string{}

	m, err := metric.New(meas, t, f, time.Now())
	if err != nil {
		log.Fatalf("Failed to create %v event", hi)
	}
	return m
}

type HubUser struct {
	ID          string            `json:"id"`
	S           EventName         `json:"-"`
	OrganizerID string            `json:"organizer_id"`
	Organizer   *faceit.Organizer `json:"organizer,omitempty"`
	UserID      string            `json:"user_id"`
	Player      *Player           `json:"-"`
	Roles       []string          `json:"roles,omitempty"`
}

func NewHubUser(s EventName) *HubUser {
	return &HubUser{S: s}
}

func (hu HubUser) String() string {
	if hu.Player == nil {
		return fmt.Sprintf(statuses[hu.S], hu.UserID)
	}

	return fmt.Sprintf(statuses[hu.S], hu.Player.Nickname)
}

func (hu HubUser) Color() int {
	return DefaultColor
}

func (hu HubUser) IconURL() string {
	if hu.Organizer == nil {
		return ""
	}

	return hu.Organizer.Avatar
}

func (hu HubUser) URL() string {
	if hu.Player != nil {
		return fmt.Sprintf("[%s](https://faceit.com/pl/players/%s)", hu.Player.Nickname, hu.UserID)
	}

	return fmt.Sprintf("https://faceit.com/pl/players/%s", hu.UserID)
}

func (hu HubUser) NewMetric() telegraf.Metric {

	f := map[string]interface{}{}
	t := map[string]string{}

	m, err := metric.New(meas, t, f, time.Now())
	if err != nil {
		log.Fatalf("Failed to create %v event", hu)
	}
	return m
}

type Role struct {
	ID            string    `json:"id"`
	S             EventName `json:"-"`
	ChatColor     string    `json:"color,omitempty"`
	Permissions   []string  `json:"permissions,omitempty"`
	OrganizerID   string    `json:"organizer_id"`
	Ranking       int       `json:"ranking,omitempty"`
	Name          string    `json:"name,omitempty"`
	RoleID        string    `json:"role_id"`
	Type          string    `json:"type,omitempty"`
	VisibleOnChat bool      `json:"visible_on_chat,omitempty"`
}

func NewRole(s EventName) *Role {
	return &Role{S: s}
}

func (r Role) Color() int {

	c, _ := strconv.Atoi(r.ChatColor)

	return c
}

func (r Role) String() string {
	return fmt.Sprintf(statuses[r.S], r.Name)
}

func (r Role) URL() string { return fmt.Sprintf("https://faceit.com/pl/organizers/%s", r.OrganizerID) }

func (r Role) IconURL() string { return "" }

func (r Role) NewMetric() telegraf.Metric {

	f := map[string]interface{}{}
	t := map[string]string{}

	m, err := metric.New(meas, t, f, time.Now())
	if err != nil {
		log.Fatalf("Failed to create %v event", r)
	}
	return m
}

type Tournament struct {
	ID                          string        `json:"tournament_id"`
	S                           EventName     `json:"-"`
	Name                        string        `json:"name"`
	FeaturedImage               string        `json:"featured_image"`
	GameID                      string        `json:"game_id"`
	Region                      string        `json:"region"`
	Status                      string        `json:"status"`
	Custom                      bool          `json:"custom"`
	InviteType                  string        `json:"invite_type"`
	PrizeType                   string        `json:"prize_type"`
	TotalPrize                  string        `json:"total_prize"`
	TeamSize                    int           `json:"team_size"`
	MinSkill                    int           `json:"min_skill"`
	MaxSkill                    int           `json:"max_skill"`
	MatchType                   string        `json:"match_type"`
	OrganizerID                 string        `json:"organizer_id"`
	WhitelistCountries          []interface{} `json:"whitelist_countries"`
	MembershipType              string        `json:"membership_type"`
	NumberOfPlayers             int           `json:"number_of_players"`
	NumberOfPlayersJoined       int           `json:"number_of_players_joined"`
	NumberOfPlayersCheckedin    int           `json:"number_of_players_checkedin"`
	NumberOfPlayersParticipants int           `json:"number_of_players_participants"`
	AnticheatRequired           bool          `json:"anticheat_required"`
	StartedAt                   int           `json:"started_at"`
	SubscriptionsCount          int           `json:"subscriptions_count"`
	FaceitURL                   string        `json:"faceit_url"`
}

func NewTournament(s EventName) *Tournament {
	return &Tournament{S: s}
}

func (to Tournament) Color() int {
	return DefaultColor
}

func (to Tournament) IconURL() string { return "" }

func (to Tournament) String() string {
	return fmt.Sprintf(statuses[to.S], to.Name)
}

func (to Tournament) URL() string {
	return fmt.Sprintf("https://www.faceit.com/en/championship/%s", to.ID)
}

func (to Tournament) NewMetric() telegraf.Metric {

	f := map[string]interface{}{}
	t := map[string]string{}

	m, err := metric.New(meas, t, f, time.Now())
	if err != nil {
		log.Fatalf("Failed to create %v event", to)
	}
	return m
}
