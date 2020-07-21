package teamspeak

import (
	"fmt"
	"log"
	"os"

	"github.com/multiplay/go-ts3"
	nts "bitbucket.org/nadia/ts-client"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
	// "strconv"
)

const MatchSectionParentChannelID = 925

type Teamspeak struct {
	Server         string
	Username       string
	Password       string
	WebQuery       string `toml:"web_query"`
	WebQueryToken  string `toml:"web_query_token"`
	VirtualServers []int  `toml:"virtual_servers"`

	client    *ts3.Client
	api       *nts.Client
	connected bool
}

func (ts *Teamspeak) Description() string {
	return "Reads metrics from a Teamspeak 3 Server via ServerQuery"
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

func (ts *Teamspeak) SampleConfig() string {
	return sampleConfig
}

func (ts *Teamspeak) Start(acc telegraf.Accumulator) error {
	
	log.Printf("[TS3] Connecting to: %s...", ts.Server)

	var err error

	ts.client, err = ts3.NewClient(ts.Server)
	if err != nil {
		return err
	}

	err = ts.client.Login(ts.Username, ts.Password)
	if err != nil {
		return err
	}

	ts.connected = true

	log.Printf("[TS3] Connected to Teamspeak 3 server instance (%s) using: %s, %s", ts.Server, ts.Username, ts.Password)

	return nil
}

func (ts *Teamspeak) Gather(acc telegraf.Accumulator) error {

	if serverList, err := ts.client.Server.List(ts3.ExtendedServerList); err != nil {
		fmt.Println(os.Stderr, "[Error] Could not iterate through Teamspeak 3 server instances")
		os.Exit(1)
	} else {
		sci, err := ts.client.Server.ServerConnectionInfo()
		if err != nil {
			return err
		}

		for _, server := range serverList {

			err := ts.client.Use(server.ID)
			if err != nil {
        		if err.Error() == "server is not running (1033)" {
					continue // ignore offline servers
				} else {
					fmt.Println(os.Stderr, "[Error] Could not select Teamspeak 3 server instance by ID")
					os.Exit(1)
				}
			}

			if err := ts.client.SetNick("[TELEGRAF] Monit"); err != nil {
				return err
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

			var format string = "teamspeak_server,port=%d,id=%d online=%d,v_clients=%d,q_clients=%d,m_clients=%d,autostart=%d,bytes_out=%d,bytes_in=%d,channels=%d,reserved_slots=%d,uptime=%d,packets_in=%d,packets_out=%d,ft_bytes_in_total=%d,ft_bytes_out_total=%d,pl_control=%f,pl_speech=%f,pl_keepalive=%f,pl_total=%f,bytes_out_speech=%d,bytes_in_speech=%d,bytes_out_control=%d,bytes_in_control=%d,bytes_out_keepalive=%d,bytes_in_keepalive=%d,packets_out_speech=%d,packets_in_speech=%d,packets_out_control=%d,packets_in_control=%d,packets_keepalive_out=%d,packets_keepalive_in=%d,avg_ping=%f\n"
			log.Printf(format, server.Port, server.ID, serverStatus, voice_clients, server.QueryClientsOnline, server.MaxClients, 
				serverAutoStart, sci.BytesSentTotal, sci.BytesReceivedTotal, server.ChannelsOnline, server.ReservedSlots, server.Uptime, 
				sci.PacketsReceivedTotal, sci.PacketsSentTotal, server.TotalBytesUploaded, server.TotalBytesDownloaded, 
				server.TotalPacketLossControl, server.TotalPacketLossSpeech, server.TotalPacketLossKeepalive, server.TotalPacketLossTotal, 
				sci.BytesSentSpeech, sci.BytesReceivedSpeech, sci.BytesSentControl, sci.BytesReceivedControl, sci.BytesSentKeepalive, 
				sci.BytesReceivedKeepalive, sci.PacketsSentSpeech, sci.PacketsReceivedSpeech, sci.PacketsSentControl, sci.PacketsReceivedControl, 
				sci.PacketsSentKeepalive, sci.PacketsReceivedKeepalive, server.TotalPing)
		}
	}

	// client := fc.NewClient()
	// channels, err := ts.client.

	// for _, vserver := range ts.VirtualServers {
	// 	ts.client.Use(vserver)

	// 	sm, err := ts.client.Server.Info()
	// 	if err != nil {
	// 		ts.connected = false
	// 		return err
	// 	}

	// 	sc, err := ts.client.Server.ServerConnectionInfo()
	// 	if err != nil {
	// 		ts.connected = false
	// 		return err
	// 	}

	// 	tags := map[string]string{
	// 		"virtual_server": strconv.Itoa(sm.ID),
	// 		"name":           sm.Name,
	// 	}

	// 	fields := map[string]interface{}{
	// 		"uptime":                 sm.Uptime,
	// 		"clients_online":         sm.ClientsOnline,
	// 		"total_ping":             sm.TotalPing,
	// 		"total_packet_loss":      sm.TotalPacketLossTotal,
	// 		"packets_sent_total":     sc.PacketsSentTotal,
	// 		"packets_received_total": sc.PacketsReceivedTotal,
	// 		"bytes_sent_total":       sc.BytesSentTotal,
	// 		"bytes_received_total":   sc.BytesReceivedTotal,
	// 	}

	// 	acc.AddFields("teamspeak", fields, tags)
	// }
	return nil
}

func init() {
	inputs.Add("teamspeak", func() telegraf.Input {
		return &Teamspeak{
			Server:         "127.0.0.1:10011",
			VirtualServers: []int{1},
		}
	})
}
