package fc

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"bitbucket.org/nadia/faceit-client"
	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/mux"
	"github.com/influxdata/telegraf"
	"github.com/thoas/go-funk"
)

type FaceitWebhook struct {
	Path       string
	Secret     string   `toml:"webhook_auth_token"`
	Token      string   `toml:"discord_bot_token"`
	APIToken   string   `toml:"faceit_api_token"`
	AdminToken string   `toml:"faceit_admin_token"`
	Excluded   []string `toml:"excluded_updates"`
	acc        telegraf.Accumulator
	discord    *discordgo.Session
	api        *faceit.Client
}

type Monit struct {
	*Match
	ticker        *time.Ticker
	statusUpdates chan EventName
	done          chan bool
}

var monits map[string]*Monit = make(map[string]*Monit)

func (f *FaceitWebhook) Register(router *mux.Router, acc telegraf.Accumulator) {

	if err := f.StartBot(); err != nil {
		log.Printf("[Error] bot err: %v", err)
	}
	f.api = faceit.New(f.APIToken, f.AdminToken)

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

	transaction, err := f.NewTransaction(body)
	if err != nil {
		log.Printf("ERROR! could not create new event: %v", err)

		w.WriteHeader(http.StatusBadRequest)
		return
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func(e Event, w *sync.WaitGroup) {
		defer w.Done()

		if err := f.updateEvent(e); err != nil {
			log.Printf("ERROR while updating from an API %v", err)
			return
		}
	}(transaction.Payload, wg)

	wg.Wait()

	wg.Add(1)
	go func(t *Transaction, w *sync.WaitGroup) {
		defer w.Done()

		time.Sleep(time.Second)
		if !funk.Contains(f.Excluded, t.Name.String()) {
			if err := f.SendDiscordAlert(t); err != nil {
				log.Println(fmt.Sprintf("[DISCORD] could not send an alert, err: %v", err))
				return
			}
		}
	}(transaction, wg)

	// f.acc.AddFields("faceit_webhooks", event.Fields(), event.Tags(), time.Unix(event.TimeStamp, 0))

	w.WriteHeader(http.StatusOK)
}

func (f *FaceitWebhook) NewTransaction(data []byte) (*Transaction, error) {

	ev := struct {
		Event EventName
	}{}
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("could not unmarshal initial transaction data, err: %v", err)
	}

	t := &Transaction{Name: ev.Event}
	if funk.Contains([]EventName{HubCreated, HubUpdated}, ev.Event) {
		t.Payload = NewHub(ev.Event)

	} else if funk.Contains([]EventName{HubRoleCreated, HubRoleDeleted, HubRoleUpdated, HubUserRoleAdded, HubUserRoleRemoved}, ev.Event) {
		t.Payload = NewRole(ev.Event)
	} else if HubUserInvited == ev.Event {
		t.Payload = NewHubInvitation(ev.Event)
	} else if funk.Contains([]EventName{HubUserAdded, HubUserRemoved}, ev.Event) {
		t.Payload = NewHubUser(ev.Event)
	} else if funk.Contains([]EventName{MatchObjectCreated, MatchStatusCancelled, MatchStatusConfiguring, MatchStatusFinished, MatchStatusReady}, ev.Event) {
		t.Payload = f.NewMatch(ev.Event)
	} else if funk.Contains([]EventName{TournamentCreated, TournamentRemoved, TournamentUpdated, TournamentCancelled, TournamentCheckin, TournamentFinished, TournamentSeeding, TournamentStarted}, ev.Event) {
		t.Payload = NewTournament(ev.Event)
	} else {
		return nil, fmt.Errorf("unknown transaction type")
	}

	if err := json.Unmarshal(data, t); err != nil {
		return nil, fmt.Errorf("could not unmarshal data second time: %v", err)
	}

	return t, nil
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

func (f *FaceitWebhook) updateEvent(e Event) error {

	wg := &sync.WaitGroup{}

	switch e.(type) {
	case *Match:

		mm := e.(*Match)

		wg.Add(1)
		go func(match *Match) {
			defer wg.Done()

			if match.IsStatus(MatchStatusCancelled, MatchStatusAborted) {
				log.Printf("[MONIT] cancelled or aborted the match: %s, skip", match.String())
				return
			}

			if err := match.SelfV2Update(f); err != nil {
				log.Printf("could not update matchV2: %s, err: %v", match.ID, err)
				return
			}

			if match.OrganizerID != PPLOrganizer {
				f.LogMsg("Organizer: %s, skipping match: %s", match.OrganizerID, match.URL())
				return
			}

			if monit, ok := monits[match.ID]; !ok {
				f.LogMsg("[MONIT] %s starting", match.String())

				monits[match.ID] = &Monit{
					Match:         match,
					statusUpdates: make(chan EventName),
					done:          make(chan bool),
					ticker:        time.NewTicker(time.Second),
				}
				wg.Add(1)
				go func(m *Match, mtis map[string]*Monit) {
					defer wg.Done()

					var ok bool
					var mo *Monit
					if mo, ok = mtis[m.ID]; !ok {
						return
					}

					for {
						select {
						case <-mo.done:
							log.Printf("\n\nstopping monitoring for match: %s\n\n", m.ID)
							return
						case s := <-mo.statusUpdates:
							if funk.Contains([]EventName{MatchStatusCancelled, MatchStatusAborted, MatchStatusFinished}, s) {
								log.Printf("the match has just finished: %s", m.ID)
								mo.done <- true
							} else {
								fmt.Printf("match: %s - update: %v", m.ID, s)
							}
						case t := <-mo.ticker.C:
							log.Printf("match [%s], status: %s, tick: %v", m.ID, m.Status.String(), t)
						}
					}
				}(match, monits)
			} else {
				if match.IsStatus(MatchStatusFinished, MatchStatusCancelled, MatchStatusAborted) {
					monit.done <- true
				} else {
					monit.statusUpdates <- match.Status
				}
			}

			wg.Add(1)
			go func(match *Match, w *sync.WaitGroup) {
				defer w.Done()

				for _, team := range match.Teams {
					pw := &sync.WaitGroup{}
					pw.Add(len(team.Roster))
					for _, p := range team.Roster {
						go func(pww *sync.WaitGroup) {
							defer pww.Done()
							if err := f.updatePlayer(p); err != nil {
								log.Printf("could not update player: %s, err: %v", p, err)
								return
							}
						}(pw)
					}
					pw.Wait()
				}
			}(mm, wg)
			wg.Wait()
		}(mm)

		// wg.Add(1)
		// go func(hub *Hub, wgg *sync.WaitGroup) {
		// 	defer wgg.Done()

		// 	if err := f.updateHub(hub); err != nil {
		// 		log.Printf("could not update hub: %s in match", hub)
		// 	}

		// }(mm.Hub, wg)
		wg.Wait()
	case *Hub:
		if err := f.updateHub(e.(*Hub)); err != nil {
			log.Printf("could not update hub: %s", e.(*Hub))
		}
	case *HubInvitation:
		wg := &sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			to := e.(HubInvitation).ToUser
			toPlayer := &Player{ID: to.ID, Nickname: to.Nickname}
			if err := f.updatePlayer(toPlayer); err != nil {
				log.Printf("could not update player: %s, err: %v", toPlayer, err)
				return
			}
		}()
		wg.Wait()
	case *HubUser:
		wg := &sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := &Player{ID: e.(*HubUser).UserID}
			if err := f.updatePlayer(p); err != nil {
				log.Printf("could not update player: %s, err: %v", p, err)
				return
			}
		}()

		wg.Add(1)
		go func(hu *HubUser) {
			defer wg.Done()
			hu.Organizer = &faceit.Organizer{ID: hu.OrganizerID}
			if err := f.updateOrganizer(hu.Organizer); err != nil {
				log.Printf("could not update organizer: %s, err: %v", hu.Organizer, err)
				return
			}

			f.LogMsg("updated organizer: %s", hu.Organizer.ID)
		}(e.(*HubUser))
		wg.Wait()
		// case Role:
		// case Tournament:
	}

	return nil
}

func (f *FaceitWebhook) updateMatch(mm *Match) error {
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func(match *Match, w *sync.WaitGroup) {
		defer w.Done()

		ttl := time.Duration(time.Minute * 2)
		m, err := f.api.GetMatchV2(match.ID, context.Background(), &ttl)
		if err != nil {
			log.Printf("could not update match: %s, err: %v", match.Hub, err)
			return
		}

		match.Results = m.Results
		match.ClientCustom = m.ClientCustom
		match.Detections = &m.MatchCustom.Overview.Detections
	}(mm, wg)

	wg.Add(1)
	go func(match *Match) {
		defer wg.Done()

		if match.Status != MatchStatusReady && match.Status != MatchStatusOngoing {
			return
		}

		if err := f.UpdateMatchLiveStats(match); err != nil {
			log.Printf("could not update match: %s", match.Hub)
			return
		}
	}(mm)

	wg.Wait()

	return nil
}

func (f *FaceitWebhook) updatePlayer(player *Player) error {
	f.LogMsg("updating player: %s", player.String())

	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func(p *Player) {
		defer wg.Done()

		ttl := time.Duration(time.Minute * 10)
		pp, err := f.api.GetPlayer(player.ID, context.Background(), &ttl)
		if err != nil {
			log.Printf("could not update player / player_info: %s", player)
			return
		}

		g := pp.Games["csgo"]
		p.Game = &g

		f.LogMsg("player info - done!")
	}(player)

	wg.Add(1)
	go func(p *Player) {
		defer wg.Done()
		ttl := time.Duration(time.Minute * 10)
		ps, err := f.api.GetPlayerGameStats(p.ID, context.Background(), &ttl)
		if err != nil {
			log.Printf("could not update player / player_stats: %s", player)
			return
		}

		p.GameStats = ps
		f.LogMsg("player stats - done!")
	}(player)

	wg.Wait()

	f.LogMsg("player updated, ELO: %s, Matches: %s", player.ELO(), player.LVL())

	return nil
}

func (f *FaceitWebhook) updateHub(h *Hub) error {
	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func(hub *Hub) {
		defer wg.Done()
		ttl := time.Duration(time.Hour * 6)
		hh, err := f.api.GetHub(hub.ID, context.Background(), &ttl)
		if err != nil {
			log.Printf("error when updating hub: %s, err: %v", hub, err)
			return
		}

		h.AvatarURL = hh.Avatar
	}(h)

	wg.Add(1)
	go func(hub *Hub) {
		defer wg.Done()
		ttl := time.Duration(time.Hour * 6)
		hrs, err := f.api.GetHubRoles(hub.ID, &faceit.PagerOptions{
			Offset: 0,
			Limit:  100,
		}, context.Background(), &ttl)
		if err != nil {
			log.Printf("error when updating hub: %s, err: %v", hub, err)
			return
		}

		for _, r := range hrs {
			b, err := json.Marshal(r)
			if err != nil {
				log.Printf("could not marshal role: %s, err: %v", r, err)
				return
			}

			nr := &Role{}
			if err := json.NewDecoder(strings.NewReader(string(b))).Decode(nr); err != nil {
				log.Printf("could not unmarshal updated role: %s, err: %v", r, err)
				continue
			}

			hub.Roles = append(hub.Roles, nr)
		}
	}(h)

	wg.Wait()

	return nil
}

func (f *FaceitWebhook) updateOrganizer(o *faceit.Organizer) error {
	var err error
	ttl := time.Duration(time.Hour * 6)
	o, err = f.api.GetOrganizer(o.ID, context.Background(), &ttl)
	if err != nil {
		return fmt.Errorf("could not update organizer: %s, err: %v", o.Name, err)
	}

	return nil
}

func (f *FaceitWebhook) LogMsg(msg string, args ...interface{}) {
	log.Printf(msg, args...)
}

func (f *FaceitWebhook) UpdateMatchLiveStats(match *Match) error {

	if match.Status != MatchStatusOngoing {
		return nil
	}

	cmdr, err := f.api.SendCommand(match, &faceit.MatchCMD{
		Name: "STATUS",
	})
	if err != nil {
		return fmt.Errorf("could not get response for match status cmd, err: %v", err)
	}

	match.LiveStats = cmdr

	return nil
}
