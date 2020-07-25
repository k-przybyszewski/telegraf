package teamspeak

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"bitbucket.org/nadia/redis-client"
	nts "bitbucket.org/nadia/ts-client"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
	"github.com/multiplay/go-ts3"
	"github.com/thoas/go-funk"
)

const MatchSectionParentChannelID = 925

var (
	verbose                       = false
	clients    map[int]*Client    = map[int]*Client{}
	channels   map[int]*Channel   = map[int]*Channel{}
	matchRooms map[int]*MatchRoom = make(map[int]*MatchRoom)
	stopCh     chan bool          = make(chan bool)
	adminsDND  map[int]time.Time  = map[int]time.Time{}
)

type MatchRoom struct {
	*Channel
	Players map[int]*Client
}

func (vs *VirtualServer) NewMatchRoom(ch *Channel) (*MatchRoom, error) {

	if err := vs.client.RegisterChannel(uint(ch.ID)); err != nil {
		return nil, err
	}

	return &MatchRoom{
		Channel: ch,
		Players: make(map[int]*Client),
	}, nil
}

type Teamspeak struct {
	Servers map[string]*VirtualServer `toml:"servers"`
}

type VirtualServer struct {
	ID            int    `toml:"id"`
	Host          string `toml:"host"`
	Nickname      string `toml:"nickname"`
	Username      string `toml:"username"`
	Password      string `toml:"password"`
	WebQueryToken string `toml:"web_query_token"`
	Interval      string `toml:"interval"`
	VoicePort     int    `toml:"voice_port"`
	QueryPort     int    `toml:"query_port"`
	WebQueryPort  int    `toml:"web_query_port"`
	HomeChannelID int    `toml:home_channel_id`

	eventsSubscriber *EventsSubscriber
	eventsProducer   *EventsProducer
	api              *nts.Client
	cache            *redis.Cache
	ci               *ts3.ConnectionInfo
	client           *ts3.Client
	ctx              context.Context
}

func (vs *VirtualServer) QueryAddr() string {
	return fmt.Sprintf("%s:%d", vs.Host, vs.QueryPort)
}

func (ts *Teamspeak) Start(acc telegraf.Accumulator) error {
	var err error
	for _, srv := range ts.Servers {
		if err = srv.Connect(); err != nil {
			continue
		}
	}

	return err
}

func (ts *Teamspeak) Stop() {

	log.Printf("[TS3] Stopping...")

	for _, srv := range ts.Servers {
		srv.Stop()
	}
}

func init() {
	nts.BaseURL = "http://10.0.0.1:10080"

	inputs.Add("teamspeak", func() telegraf.Input {
		ts := &Teamspeak{}
		return ts
	})
}

func (ts *Teamspeak) Description() string {
	return "Reads metrics from a Teamspeak 3 VirtualServer via ServerQuery"
}

const sampleConfig = `
  ## Server address for Teamspeak 3 ServerQuery
  # server = "127.0.0.1:10011"
  ## Username for ServerQuery
  username = "serverqueryuser"
  ## Password for ServerQuery
  password = "secret"
  ## Array of virtual servers
  # virtual_servers = [1]
`

//
func (ts *Teamspeak) SampleConfig() string {
	return sampleConfig
}

func (vs *VirtualServer) Stop() {
	vs.eventsProducer.Stop()
	vs.client.Close()
	vs.cache.Close()
}

func (vs *VirtualServer) Connect() error {

	log.Printf("[TS3] Connecting to: %s..., ID: %d", vs.Host, vs.ID)

	vs.api = nts.NewClient(vs.WebQueryToken, false)

	var err error
	vs.client, err = ts3.NewClient(vs.QueryAddr())
	if err != nil {
		return err
	}

	if err := vs.client.UsePort(vs.VoicePort); err != nil {
		return err
	}

	if err := vs.client.Login(vs.Username, vs.Password); err != nil {
		return err
	}

	vs.client.SetNick(vs.Nickname)

	vs.ci, err = vs.client.Whoami()
	if err != nil {
		return err
	}

	if _, err := vs.client.ExecCmd(ts3.NewCmd("clientmove").WithArgs(ts3.NewArg("clid", vs.ci.ClientID), ts3.NewArg("cid", vs.HomeChannelID))); err != nil {
		return err
	}

	if err := vs.client.SetChannelCommander(true); err != nil {
		return err
	}

	redis.Cfg = &redis.Config{
		Host: "10.0.0.1",
		Port: 6379,
	}
	vs.cache = redis.New(verbose)
	vs.ctx = context.Background()

	vs.eventsProducer = vs.NewEventsProducer()
	if err := vs.eventsProducer.Start(); err != nil {
		return err
	}
	vs.eventsSubscriber = vs.NewEventsSubscriber()

	vs.log("started events service")

	log.Printf("[TS3] Connected to Teamspeak 3 server instance (%s) using: %s, %s", vs.Host, vs.Username, vs.Password)

	return nil
}

func (ts *Teamspeak) Gather(acc telegraf.Accumulator) error {
	var err error
	for _, t := range ts.Servers {
		err = t.Gather(acc)
	}

	return err
}

func (vs *VirtualServer) Gather(acc telegraf.Accumulator) error {
	if !vs.client.IsConnected() {
		return fmt.Errorf("[TS3] Error! Not connected")
	}

	server, err := vs.client.Server.Info()
	if err != nil {
		return err
	}
	sci, err := vs.client.Server.ServerConnectionInfo()
	if err != nil {
		return err
	}

	tags := map[string]string{
		"id":   fmt.Sprintf("%d", vs.ID),
		"host": vs.Host,
		"name": fmt.Sprintf("%s", server.Name),
		"port": fmt.Sprintf("%d", vs.VoicePort),
	}

	var serverAutoStart int = 0

	if server.AutoStart == true {
		serverAutoStart = 1
	}

	var voice_clients = server.ClientsOnline - server.QueryClientsOnline
	var serverStatus int = 0

	if server.Status == "online" {
		serverStatus = 1
	} else {
		serverStatus = 2
	}

	fields := map[string]interface{}{
		"online":                serverStatus,
		"v_clients":             voice_clients,
		"q_clients":             server.QueryClientsOnline,
		"m_clients":             server.MaxClients,
		"autostart":             serverAutoStart,
		"bytes_out":             sci.BytesSentTotal,
		"bytes_in":              sci.BytesReceivedTotal,
		"channels":              server.ChannelsOnline,
		"reserved_slots":        server.ReservedSlots,
		"uptime":                server.Uptime,
		"packets_in":            sci.PacketsReceivedTotal,
		"packets_out":           sci.PacketsSentTotal,
		"ft_bytes_in_total":     server.TotalBytesUploaded,
		"ft_bytes_out_total":    server.TotalBytesDownloaded,
		"pl_control":            server.TotalPacketLossControl,
		"pl_speech":             server.TotalPacketLossSpeech,
		"pl_keepalive":          server.TotalPacketLossKeepalive,
		"pl_total":              server.TotalPacketLossTotal,
		"bytes_out_speech":      sci.BytesSentSpeech,
		"bytes_in_speech":       sci.BytesReceivedSpeech,
		"bytes_out_control":     sci.BytesSentControl,
		"bytes_in_control":      sci.BytesReceivedControl,
		"bytes_out_keepalive":   sci.BytesSentKeepalive,
		"bytes_in_keepalive":    sci.BytesReceivedKeepalive,
		"packets_out_speech":    sci.PacketsSentSpeech,
		"packets_in_speech":     sci.PacketsReceivedSpeech,
		"packets_out_control":   sci.PacketsSentControl,
		"packets_in_control":    sci.PacketsReceivedControl,
		"packets_keepalive_out": sci.PacketsSentKeepalive,
		"packets_keepalive_in":  sci.PacketsReceivedKeepalive,
		"avg_ping":              server.TotalPing,
	}

	acc.AddFields("ts", fields, tags)

	groups, err := vs.GroupList()
	if err != nil {
		return err
	}

	clients, err = vs.ClientList("-uid", "-away", "-voice", "-times", "-groups", "-info", "-country", "-ip", "-badges")
	if err != nil {
		return err
	}

	channels, err = vs.ChannelList("-topic", "-flags", "-voice", "-limits", "-icon", "-secondsempty")
	if err != nil {
		return err
	}

	if err := vs.processLists(acc, clients, channels, groups); err != nil {
		return err
	}

	return nil
}

type Client struct {
	ID int `ms:"clid"`
}

func (vs *VirtualServer) ClientList(options ...string) (map[int]*Client, error) {
	var cls []Client
	_, err := vs.client.ExecCmd(ts3.NewCmd("clientlist").WithOptions(options...).WithResponse(&cls))
	if err != nil {
		return nil, err
	}

	return funk.Map(cls, func(cl Client) (int, *Client) {
		return cl.ID, &cl
	}).(map[int]*Client), nil
}

type Channel struct {
	ID                                   int    `ms:"cid"`
	ChannelBannerGfxURL                  string `ms:"channel_banner_gfx_url"`
	ChannelBannerMode                    string `ms:"channel_banner_mode"`
	ChannelCodec                         string `ms:"channel_codec"`
	ChannelCodecIsUnencrypted            bool   `ms:"channel_codec_is_unencrypted"`
	ChannelCodecLatencyFactor            string `ms:"channel_codec_latency_factor"`
	ChannelCodecQuality                  int    `ms:"channel_codec_quality"`
	ChannelDeleteDelay                   int    `ms:"channel_delete_delay"`
	ChannelDescription                   string `ms:"channel_description"`
	ChannelFilepath                      string `ms:"channel_filepath"`
	ChannelFlagDefault                   string `ms:"channel_flag_default"`
	ChannelFlagMaxclientsUnlimited       string `ms:"channel_flag_maxclients_unlimited"`
	ChannelFlagMaxfamilyclientsInherited string `ms:"channel_flag_maxfamilyclients_inherited"`
	ChannelFlagMaxfamilyclientsUnlimited string `ms:"channel_flag_maxfamilyclients_unlimited"`
	ChannelFlagPassword                  string `ms:"channel_flag_password"`
	ChannelFlagPermanent                 int    `ms:"channel_flag_permanent"`
	ChannelFlagSemiPermanent             int    `ms:"channel_flag_semi_permanent"`
	ChannelForcedSilence                 int    `ms:"channel_forced_silence"`
	ChannelIconID                        int    `ms:"channel_icon_id"`
	ChannelMaxclients                    int    `ms:"channel_maxclients"`
	ChannelMaxfamilyclients              int    `ms:"channel_maxfamilyclients"`
	ChannelName                          string `ms:"channel_name"`
	ChannelNamePhonetic                  string `ms:"channel_name_phonetic"`
	ChannelNeededTalkPower               string `ms:"channel_needed_talk_power"`
	ChannelOrder                         int    `ms:"channel_order"`
	ChannelPassword                      string `ms:"channel_password"`
	ChannelSecuritySalt                  string `ms:"channel_security_salt"`
	ChannelTopic                         string `ms:"channel_topic"`
	ChannelUniqueIdentifier              string `ms:"channel_unique_identifier"`
	ParentID                             int    `ms:"pid"`
	SecondsEmpty                         int    `ms:"seconds_empty"`
	TotalClients                         int    `ms:"total_clients,omitempty"`
}

func (vs *VirtualServer) ChannelList(options ...string) (map[int]*Channel, error) {
	var chs []*Channel
	_, err := vs.client.ExecCmd(ts3.NewCmd("channellist").WithOptions(options...).WithResponse(&chs))
	if err != nil {
		return nil, err
	}

	return funk.Map(chs, func(ch *Channel) (int, *Channel) {
		return ch.ID, ch
	}).(map[int]*Channel), nil
}

// Group represents a virtual server group.
type Group struct {
	ID                int `ms:"sgid"`
	Name              string
	Type              int
	IconID            int
	Saved             bool `ms:"savedb"`
	SortID            int
	NameMode          int
	ModifyPower       int `ms:"n_modifyp"`
	MemberAddPower    int `ms:"n_member_addp"`
	MemberRemovePower int `ms:"n_member_addp"`
}

func (vs *VirtualServer) GroupList() (map[int]*Group, error) {
	var groups []*Group
	_, err := vs.client.ExecCmd(ts3.NewCmd("servergrouplist").WithResponse(&groups))
	if err != nil {
		return nil, err
	}

	return funk.Map(groups, func(g *Group) (int, *Group) {
		return g.ID, g
	}).(map[int]*Group), nil
}

func (vs *VirtualServer) ChannelInfo(ch *Channel) error {
	_, err := vs.client.ExecCmd(ts3.NewCmd("channelinfo").WithResponse(ch))
	return err
}

func (vs *VirtualServer) ClientInfo(c *Client) error {
	_, err := vs.client.ExecCmd(ts3.NewCmd("clientinfo").WithArgs(ts3.NewArg("clid", c.ID)).WithResponse(c))
	return err
}

func (vs *VirtualServer) processLists(acc telegraf.Accumulator, clients map[int]*Client, channels map[int]*Channel, groups map[int]*Group) error {

	tags := map[string]string{
		"cid":  "0",
		"name": "",
	}

	parents := make(map[int]*Channel)
	for _, ch := range channels {
		if ch.ParentID != MatchSectionParentChannelID {
			continue
		}

		parents[ch.ID] = ch
	}

	for _, ch := range channels {
		if _, ok := parents[ch.ParentID]; !ok {
			continue
		}

		r, err := vs.NewMatchRoom(ch)
		if err != nil {
			continue
		}
		matchRooms[ch.ID] = r
	}

	wg := &sync.WaitGroup{}
	for _, c := range clients {
		ctx, cancel := context.WithTimeout(vs.ctx, time.Second*3)
		go func() {
			for {
				select {
				case <-stopCh:
					log.Println("cancelling all API requests")

					cancel()
					return
				}
			}
		}()

		wg.Add(1)
		go func(clid int, chs map[int]*Channel, rooms map[int]*MatchRoom, w *sync.WaitGroup, canc context.CancelFunc) {
			defer func() {
				w.Done()
				canc()
			}()

			ci, err := vs.api.QWebGetPlayerInfo(clid, ctx)
			if err != nil {
				log.Printf("could not get client info for client ID: %d, err: %v", c.ID, err)
				return
			}

			sGroups := []int{}
			sGroups = append(sGroups, ci.ClientServerGroups...)
			if ci.ClientType != nts.NormalClient || funk.Contains(sGroups, int(nts.DJ)) {
				return
			}

			ptags := map[string]string{
				"ip":       ci.ConnectionClientIP,
				"nickname": ci.ClientNickname,
			}

			if ch, ok := chs[ci.CID]; ok {
				ptags["channel_name"] = ch.ChannelName
			}

			if mr, ok := rooms[ci.CID]; ok {
				mr.Players[ci.ClientID] = c
				ptags["match_room"] = mr.Channel.ChannelName
			}

			pfields := make(map[string]interface{})
			pfields["client_ip"] = ci.ConnectionClientIP
			pfields["client_input_hardware"] = ci.ClientInputHardware
			pfields["client_output_hardware"] = ci.ClientOutputHardware
			pfields["client_input_muted"] = ci.ClientInputMuted
			pfields["client_output_muted"] = ci.ClientOutputMuted
			pfields["client_outputonly_muted"] = ci.ClientOutputonlyMuted
			pfields["client_idle_time"] = ci.ClientIdleTime.Duration.Seconds()
			pfields["client_is_talker"] = ci.ClientIsTalker
			pfields["client_total_bytes_downloaded"] = ci.ClientTotalBytesDownloaded
			pfields["client_total_bytes_uploaded"] = ci.ClientTotalBytesUploaded
			pfields["connection_bandwidth_received_last_minute_total"] = ci.ConnectionBandwidthReceivedLastMinuteTotal
			pfields["connection_bandwidth_sent_last_minute_total"] = ci.ConnectionBandwidthSentLastMinuteTotal
			pfields["connection_bandwidth_received_last_second_total"] = ci.ConnectionBandwidthReceivedLastSecondTotal
			pfields["connection_bandwidth_sent_last_second_total"] = ci.ConnectionBandwidthSentLastSecondTotal
			pfields["connection_bytes_received_total"] = ci.ConnectionBytesReceivedTotal
			pfields["connection_bytes_sent_total"] = ci.ConnectionBytesSentTotal
			pfields["connection_packets_received_total"] = ci.ConnectionPacketsReceivedTotal
			pfields["connection_packets_sent_total"] = ci.ConnectionPacketsSentTotal

			acc.AddFields("clients", pfields, ptags)

			if vs.IsAdmin(ci) {
				log.Printf("[TS3] procesing admin: %s", ci.ClientNickname)

				atags := map[string]string{
					"ip":       ci.ConnectionClientIP,
					"nickname": ci.ClientNickname,
				}

				afields := make(map[string]interface{})

				var ok bool
				var dndAt time.Time
				if dndAt, ok = adminsDND[ci.ClientID]; !ok && vs.IsDND(ci) {
					afields["dnd_seconds"] = time.Now().Sub(dndAt).Seconds()
				} else if ok && !vs.IsDND(ci) {
					afields["dnd_seconds"] = 0
					delete(adminsDND, ci.ClientID)
				}

				acc.AddFields("admins", afields, atags)
			}

		}(c.ID, channels, matchRooms, wg, cancel)
	}

	for cid, mch := range matchRooms {
		if mch.TotalClients == 0 || len(mch.Players) == 0 {
			continue
		}

		tags["cid"] = fmt.Sprintf("%d", cid)
		tags["name"] = mch.ChannelName

		fields := map[string]interface{}{
			"players_count": len(mch.Players),
		}

		acc.AddFields("rooms", fields, tags)
	}

	wg.Wait()

	return nil
}

func (vs *VirtualServer) IsAdmin(pi *nts.PlayerInfo) bool {
	for _, ag := range nts.AdminGroups {
		if funk.Contains(pi.ClientServerGroups, ag) {
			return true
		}
	}

	return false
}

func (vs *VirtualServer) IsDND(pi *nts.PlayerInfo) bool {
	return funk.Contains(pi.ClientServerGroups, nts.DNDGroup)
}

func (vs *VirtualServer) log(format string, args ...interface{}) {
	format = "[TS3][VirtualServer] I! " + format

	if len(args) > 0 {
		fmt.Printf(format, args)
		return
	}

	log.Println(format)
}

func (vs *VirtualServer) logError(format string, err error, args ...interface{}) {
	format = "[TS3][VirtualServer] E! " + format + ": %v!"

	if len(args) > 0 {
		fmt.Printf(format, args)
		return
	}

	log.Println(format)
}
