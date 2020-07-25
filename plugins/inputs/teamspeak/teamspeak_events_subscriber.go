package teamspeak

import "bitbucket.org/nadia/redis-client"

type EventsSubscriber struct {
	cache *redis.Cache
}

func (vs *VirtualServer) NewEventsSubscriber() *EventsSubscriber {
	return &EventsSubscriber{
		cache: vs.cache,
	}
}

func (es *EventsSubscriber) Subscribe(name string) error {

}
