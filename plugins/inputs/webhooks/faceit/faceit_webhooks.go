package faceit

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"reflect"
	"strings"
	"time"

	"bitbucket.org/nadia/faceit-client"
	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/mux"
	"github.com/influxdata/telegraf"
	"github.com/thoas/go-funk"
)

type FaceitWebhook struct {
	Path       string
	Secret     string `toml:"webhook_auth_token"`
	Token      string `toml:"discord_bot_token"`
	APIToken   string `toml:"faceit_api_token"`
	AdminToken string `toml:"faceit_admin_token"`
	acc        telegraf.Accumulator
	discord    *discordgo.Session
	api        *faceit.Client
}

func (f *FaceitWebhook) Register(router *mux.Router, acc telegraf.Accumulator) {

	log.Printf("[DISCORD] bot token: %s", f.Token)

	go func() {
		if err := f.StartBot(); err != nil {
			log.Printf("[Error] bot err: %v", err)
		}
	}()
	// f.api = faceit.New(f.APIToken, f.AdminToken)

	router.HandleFunc(f.Path, f.eventHandler).Methods("POST")

	log.Printf("I! Started the webhooks_faceit on %s\n", f.Path)
	f.acc = acc
}

func (f *FaceitWebhook) eventHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Println(fmt.Sprintf("[FACEIT] new event: %s", string(body)))

	params := r.URL.Query()
	token := params.Get("token")

	var header = strings.Trim(r.Header.Get("Authorization"), " \n\t\r")
	headerSplit := strings.Split(header, "Bearer ")
	if token == "" && len(headerSplit) == 2 {
		token = headerSplit[1]
	}

	if token != f.Secret {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	event, err := f.NewEvent(body)
	if err != nil {
		log.Printf("ERROR! could not create new event: %v", err)

		w.WriteHeader(http.StatusBadRequest)
		return
	}

	go func() {
		// ignoredEvents := []EventName{HubUpdated}
		// if !funk.Contains(ignoredEvents, EventName(event.String())) {
		if err := f.SendDiscordAlert(event); err != nil {
			log.Println(fmt.Sprintf("[DISCORD] could not send an alert, err: %v", err))
			return
		}
		// }
	}()

	// f.acc.AddFields("faceit_webhooks", event.Fields(), event.Tags(), time.Unix(event.TimeStamp, 0))

	w.WriteHeader(http.StatusOK)
}

func (f *FaceitWebhook) NewEvent(data []byte) (Event, error) {

	t := &TransactionEvent{}
	if err := json.Unmarshal(data, t); err != nil {
		return nil, err
	}

	var o Event
	if funk.Contains([]EventName{HubCreated, HubUpdated}, t.Name) {
		o = Hub{}
	} else if funk.Contains([]EventName{HubRoleCreated, HubRoleDeleted, HubRoleUpdated, HubUserRoleAdded, HubUserRoleRemoved}, t.Name) {
		o = &Role{}
	} else if HubUserInvited == t.Name {
		o = &HubInvitation{}
	} else if funk.Contains([]EventName{HubUserAdded, HubUserRemoved}, t.Name) {
		o = &HubUser{}
	} else if funk.Contains([]EventName{MatchObjectCreated, MatchStatusCancelled, MatchStatusConfiguring, MatchStatusFinished, MatchStatusReady}, t.Name) {
		o = NewMatch(t.Name)
	} else if funk.Contains([]EventName{TournamentCreated, TournamentRemoved, TournamentUpdated, TournamentCancelled, TournamentCheckin, TournamentFinished, TournamentSeeding, TournamentStarted}, t.Name) {
		o = &Tournament{}
	}

	log.Printf("\n %s \n\n %v \n\n", t.Name, reflect.TypeOf(o))

	// log.Println(fmt.Sprintf("[FACEIT] new event: %s", (t.Name.String())))

	return o, nil
}


// TimeIn returns the time in UTC if the name is "" or "UTC".
// It returns the local time if the name is "Local".
// Otherwise, the name is taken to be a location name in
// the IANA Time Zone database, such as "Africa/Lagos".
func TimeIn(t time.Time, name string) (time.Time, error) {
	loc, err := time.LoadLocation(name)
	if err == nil {
		t = t.In(loc)
	}
	return t, err
}
