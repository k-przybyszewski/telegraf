package faceit

import (
	"fmt"
	"log"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/metric"
)

const meas = "faceit_webhooks"

type EventName string

func (en EventName) String() string {
	return string(en)
}

type Event interface {
	NewMetric() telegraf.Metric
	String() string
	URL() string
}

const (
	HubCreated EventName = EventName("hub_created")
	HubUpdated EventName = EventName("hub_updated")

	HubRoleCreated EventName = EventName("hub_role_created")
	HubRoleDeleted EventName = EventName("hub_role_deleted")
	HubRoleUpdated EventName = EventName("hub_role_updated")

	HubUserInvited EventName = EventName("hub_user_added")
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

	TournamentCreated   EventName = EventName("tournament_object_created")
	TournamentRemoved   EventName = EventName("tournament_object_removed")
	TournamentUpdated   EventName = EventName("tournament_object_updated")
	TournamentCancelled EventName = EventName("tournament_status_cancelled")
	TournamentCheckin   EventName = EventName("tournament_status_checkin")
	TournamentFinished  EventName = EventName("tournament_status_finished")
	TournamentSeeding   EventName = EventName("tournament_status_seeding")
	TournamentStarted   EventName = EventName("tournament_status_started")
)

type TransactionEvent struct {
	TransactionID string    `json:"transaction_id"`
	Name          EventName `json:"event"`
	EventID       string    `json:"event_id"`
	ThirdPartyID  string    `json:"third_party_id"`
	AppID         string    `json:"app_id"`
	Timestamp     time.Time `json:"timestamp"`
	RetryCount    int       `json:"retry_count"`
	Version       int       `json:"version"`
}

func (t *TransactionEvent) String() string {
	return string(t.Name)
}

type Match struct {
	ID          string `json:"id"`
	OrganizerID string `json:"organizer_id"`
	Region      string `json:"region"`
	Game        string `json:"game"`
	Version     int    `json:"version"`
	Entity      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"entity"`
	Teams []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Avatar     string `json:"avatar"`
		LeaderID   string `json:"leader_id"`
		CoLeaderID string `json:"co_leader_id"`
		Roster     []struct {
			ID                string `json:"id"`
			Nickname          string `json:"nickname"`
			Avatar            string `json:"avatar"`
			GameID            string `json:"game_id"`
			GameName          string `json:"game_name"`
			GameSkillLevel    int    `json:"game_skill_level"`
			Membership        string `json:"membership"`
			AnticheatRequired bool   `json:"anticheat_required"`
		} `json:"roster"`
		Substitutions int         `json:"substitutions"`
		Substitutes   interface{} `json:"substitutes"`
	} `json:"teams"`
	Status       EventName `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	ConfiguredAt time.Time `json:"configured_at,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
}

func NewMatch(en EventName) *Match {
	return &Match{Status: en}
}

func (mt Match) String() string {
	if len(mt.Teams) == 0 {
		return fmt.Sprintf("[HUB: %s - Match: %s] New Match", mt.Entity.Name, mt.ID)
	}

	return fmt.Sprintf("[HUB: %s - Match: %s] %s vs %s", mt.Entity.Name, mt.ID, mt.Teams[0].Name, mt.Teams[1].Name)
}

func (mt Match) URL() string { return fmt.Sprintf("https://faceit.com/pl/csgo/room/%s", mt.ID) }

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

type Hub struct {
	ID          string `json:"id"`
	OrganizerID string `json:"organizer_id"`
	Roles       []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Permissions   []string `json:"permissions"`
		Ranking       int      `json:"ranking"`
		Color         string   `json:"color"`
		Type          string   `json:"type"`
		VisibleOnChat bool     `json:"visible_on_chat"`
	} `json:"roles"`
}

func (h Hub) String() string {
	return h.ID
}

func (h Hub) URL() string { return fmt.Sprintf("https://faceit.com/pl/hub/%s", h.ID) }

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
	ID          string `json:"id"`
	OrganizerID string `json:"organizer_id"`
	Type        string `json:"type"`
	FromUser    struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	} `json:"from_user"`
	ToUser struct {
		ID       string `json:"id"`
		Nickname string `json:"nickname"`
	} `json:"to_user"`
}

func (hi HubInvitation) String() string {
	fromURL := fmt.Sprintf("https://faceit.com/pl/players/%s", hi.FromUser.ID)
	toURL := fmt.Sprintf("https://faceit.com/pl/players/%s", hi.ToUser.ID)

	return fmt.Sprintf("type: %s, from: %s [%s], to: %s [%s]", hi.Type, fromURL, hi.FromUser.Nickname, toURL, hi.ToUser.Nickname)
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
	ID          string   `json:"id"`
	OrganizerID string   `json:"organizer_id"`
	UserID      string   `json:"user_id"`
	Roles       []string `json:"roles,omitempty"`
}

func (hu HubUser) String() string {
	return fmt.Sprintf("[Organizer: %s, User: %s]", hu.OrganizerID, hu.UserID)
}

func (hu HubUser) URL() string { return fmt.Sprintf("https://faceit.com/pl/players/%s", hu.UserID) }

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
	ID          string   `json:"id"`
	OrganizerID string   `json:"organizer_id"`
	RoleID      string   `json:"role_id"`
	RoleName    string   `json:"role_name,omitempty"`
	Type        string   `json:"type,omitempty"`
	Ranking     int      `json:"ranking,omitempty"`
	Color       string   `json:"color,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

func (r Role) String() string {
	return fmt.Sprintf("Organizer: %s, Role: %s", r.OrganizerID, r.RoleName)
}

func (r Role) URL() string { return r.RoleID }

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
	ID          string `json:"id"`
	OrganizerID string `json:"organizer_id"`
}

func (to Tournament) String() string {
	return fmt.Sprintf("Organizer: %s, Tournament: %s", to.OrganizerID, to.ID)
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
