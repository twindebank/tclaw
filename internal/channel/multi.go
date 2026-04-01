package channel

import (
	"context"
	"sync"
)

// ChannelMap builds a map keyed by each channel's Info().ID.
func ChannelMap(chs ...Channel) map[ChannelID]Channel {
	m := make(map[ChannelID]Channel, len(chs))
	for _, ch := range chs {
		m[ch.Info().ID] = ch
	}
	return m
}

// FanIn reads from all channels concurrently, tagging each message
// with its source ChannelID. The returned channel closes when all
// input channels are drained or ctx is cancelled.
func FanIn(ctx context.Context, channels map[ChannelID]Channel) <-chan TaggedMessage {
	out := make(chan TaggedMessage)

	var wg sync.WaitGroup
	for id, ch := range channels {
		wg.Add(1)
		go func(id ChannelID, ch Channel) {
			defer wg.Done()
			for msg := range ch.Messages(ctx) {
				select {
				case out <- TaggedMessage{
					ChannelID:  id,
					Text:       msg,
					SourceInfo: &MessageSourceInfo{Source: SourceUser},
				}:
				case <-ctx.Done():
					return
				}
			}
		}(id, ch)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// MergeFanIns combines multiple TaggedMessage sources into a single channel.
// The returned channel closes when all sources are drained or ctx is cancelled.
func MergeFanIns(ctx context.Context, sources ...<-chan TaggedMessage) <-chan TaggedMessage {
	out := make(chan TaggedMessage)

	var wg sync.WaitGroup
	for _, src := range sources {
		if src == nil {
			continue
		}
		wg.Add(1)
		go func(ch <-chan TaggedMessage) {
			defer wg.Done()
			for {
				select {
				case msg, ok := <-ch:
					if !ok {
						return
					}
					select {
					case out <- msg:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(src)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// InfoAll returns Info for every channel in the map.
func InfoAll(channels map[ChannelID]Channel) []Info {
	infos := make([]Info, 0, len(channels))
	for _, ch := range channels {
		infos = append(infos, ch.Info())
	}
	return infos
}
