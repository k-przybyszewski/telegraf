package teamspeak

import (
	"context"
	"fmt"
	"log"
	"time"

	"bitbucket.org/nadia/redis-client"
	nts "bitbucket.org/nadia/ts-client"
	"github.com/multiplay/go-ts3"
)

const DefaultPubSubChannelName = "teamspeak.events.%s"

type EventsProducer struct {
	cache  *redis.Cache
	client *ts3.Client
	api    *nts.Client
	ticker *time.Ticker
	done   chan bool
	ctx    context.Context
	stop   context.CancelFunc
}

// Listener - Listener
func (vs *VirtualServer) NewEventsProducer() *EventsProducer {

	interval, _ := time.ParseDuration(vs.Interval)

	el := &EventsProducer{
		cache:  vs.cache,
		client: vs.client,
		api:    vs.api,
		ticker: time.NewTicker(interval),
	}

	el.ctx, el.stop = context.WithCancel(vs.ctx)

	return el
}

func (e *EventsProducer) Start() error {

	e.log("starting events service")

	if err := e.register(ts3.ServerEvents, ts3.ChannelEvents, ts3.TokenUsedEvents, ts3.TextServerEvents); err != nil {
		return err
	}

	go func(el *EventsProducer) {
		for {
			select {
			case <-el.ctx.Done():
				el.log("Cancelling instance context...")
				el.log("Stoping all services.")

				el.stop()
				return
			case n := <-el.client.Notifications():
				not := nts.Notification(n)
				if err := el.cache.Publish(fmt.Sprintf(DefaultPubSubChannelName, n.Type), not); err != nil {
					el.logError("", err)
				}
			}
		}
	}(e)

	return nil
}

func (e *EventsProducer) Stop() {
	e.log("stop...")

	e.stop()
}

func (e *EventsProducer) log(format string, args ...interface{}) {
	format = "[TS3][Events] I! " + format + "\n"

	if len(args) > 0 {
		fmt.Printf(format, args)
		return
	}

	log.Println(format)
}

func (e *EventsProducer) logError(format string, err error, args ...interface{}) {
	format = "[TS3][Events] E! " + format + ": %v!"

	args = append(args, err)

	if len(args) > 0 {
		fmt.Printf(format, args)
		return
	}

	log.Println(format)

	return
}

func (e *EventsProducer) register(cats ...ts3.NotifyCategory) error {

	e.log("Registering for server events")

	for _, c := range cats {
		if err := e.client.Register(c); err != nil {
			e.logError("register for TeamSpeak events error!", err)
			continue
		}
	}

	e.log("Events registration done")

	return nil
}
