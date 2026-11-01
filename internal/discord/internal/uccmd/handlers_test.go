package uccmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/HectorVeraO/chatisimo/internal/discord/internal/uccmd"
	"github.com/HectorVeraO/chatisimo/internal/uccalendar"
)

type stubUCService struct{}

func (stubUCService) Check(_ context.Context, _ uccalendar.Query) (uccalendar.Report, error) {
	return uccalendar.Report{}, nil
}

func (stubUCService) GetGuildConfig(
	_ context.Context,
	_ uccalendar.GuildScope,
) (uccalendar.GuildConfig, error) {
	return uccalendar.GuildConfig{}, nil
}

func (stubUCService) UpdateGuildConfig(
	_ context.Context,
	_ uccalendar.GuildScope,
	_ uccalendar.GuildConfigUpdate,
) (uccalendar.GuildConfig, error) {
	return uccalendar.GuildConfig{}, nil
}

func (stubUCService) ClearGuildConfig(
	_ context.Context,
	_ uccalendar.GuildScope,
	_ uccalendar.ClearTarget,
) (uccalendar.GuildConfig, error) {
	return uccalendar.GuildConfig{}, nil
}

func TestNewAggregatesAllMissingDependencies(t *testing.T) {
	t.Parallel()

	_, err := uccmd.New(nil, nil)
	if err == nil {
		t.Fatal("New(nil, nil) succeeded, want error")
	}
	for _, want := range []string{"service", "location"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	handlers, err := uccmd.New(stubUCService{}, time.UTC)
	if err != nil || handlers == nil {
		t.Fatalf("New(valid) = %v, %v, want non-nil handlers", handlers, err)
	}
}

func mustUnmarshalSlashData(t *testing.T, raw string) discord.SlashCommandInteractionData {
	t.Helper()

	var data discord.SlashCommandInteractionData
	err := json.Unmarshal([]byte(raw), &data)
	if err != nil {
		t.Fatalf("Unmarshal SlashCommandInteractionData: %v", err)
	}

	return data
}

func TestParseConfigUpdateAndCheckOptionsRejectMalformedAndUnauthorizedResolvedPayloads(t *testing.T) {
	t.Parallel()

	const (
		memberID  = "100000000000000001"
		botID     = "100000000000000002"
		outsider  = "100000000000000003"
		textChan  = "300000000000000001"
		newsChan  = "300000000000000002"
		voiceChan = "300000000000000003"
	)

	validPayload := `{
		"id":"1","name":"uc-config","type":1,
		"resolved":{
			"users":{
				"` + memberID + `":{"id":"` + memberID + `","username":"fan","discriminator":"0","bot":false}
			},
			"members":{
				"` + memberID + `":{"permissions":"0"}
			},
			"channels":{
				"` + newsChan + `":{"id":"` + newsChan + `","name":"alerts","type":5,"permissions":"0"}
			}
		},
		"options":[{
			"name":"set","type":1,
			"options":[
				{"name":"friend","type":6,"value":"` + memberID + `"},
				{"name":"channel","type":7,"value":"` + newsChan + `"},
				{"name":"locale","type":3,"value":"en-US"}
			]
		}]
	}`
	validData := mustUnmarshalSlashData(t, validPayload)
	update, err := uccmd.ParseConfigUpdateOptions(validData)
	if err != nil {
		t.Fatalf("ParseConfigUpdateOptions(valid): %v", err)
	}
	if update.FriendUserID == nil || *update.FriendUserID != memberID {
		t.Errorf("FriendUserID = %v, want %q", update.FriendUserID, memberID)
	}
	if update.ChannelID == nil || *update.ChannelID != newsChan {
		t.Errorf("ChannelID = %v, want %q", update.ChannelID, newsChan)
	}
	if update.Locale == nil || *update.Locale != uccalendar.LocaleENUS {
		t.Errorf("Locale = %v, want %q", update.Locale, uccalendar.LocaleENUS)
	}

	invalidConfigCases := []struct {
		name    string
		payload string
		wantErr error
	}{
		{
			name: "rejects bot member as friend",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"resolved":{
					"users":{"` + botID + `":{"id":"` + botID + `","username":"bot","discriminator":"0","bot":true}},
					"members":{"` + botID + `":{"permissions":"0"}}
				},
				"options":[{"name":"set","type":1,"options":[{"name":"friend","type":6,"value":"` + botID + `"}]}]
			}`,
			wantErr: uccmd.ErrFriendIsBot,
		},
		{
			name: "rejects user absent from guild members even if bot in another guild",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"resolved":{
					"users":{"` + outsider + `":{"id":"` + outsider + `","username":"other","discriminator":"0","bot":true}}
				},
				"options":[{"name":"set","type":1,"options":[{"name":"friend","type":6,"value":"` + outsider + `"}]}]
			}`,
			wantErr: uccmd.ErrInvalidFriend,
		},
		{
			name: "rejects non-string JSON value on friend option without panicking",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"options":[{"name":"set","type":1,"options":[{"name":"friend","type":6,"value":12345}]}]
			}`,
			wantErr: uccmd.ErrInvalidFriend,
		},
		{
			name: "rejects wrong option type on friend option",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"options":[{"name":"set","type":1,"options":[{"name":"friend","type":3,"value":"` + memberID + `"}]}]
			}`,
			wantErr: uccmd.ErrInvalidFriend,
		},
		{
			name: "rejects voice channel type",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"resolved":{
					"channels":{"` + voiceChan + `":{"id":"` + voiceChan + `","name":"voice","type":2,"permissions":"0"}}
				},
				"options":[{"name":"set","type":1,"options":[{"name":"channel","type":7,"value":"` + voiceChan + `"}]}]
			}`,
			wantErr: uccmd.ErrInvalidChannel,
		},
		{
			name: "rejects channel omitted from resolved guild channels",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"options":[{"name":"set","type":1,"options":[{"name":"channel","type":7,"value":"` + textChan + `"}]}]
			}`,
			wantErr: uccmd.ErrInvalidChannel,
		},
		{
			name: "rejects unsupported locale value",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"options":[{"name":"set","type":1,"options":[{"name":"locale","type":3,"value":"pt-BR"}]}]
			}`,
			wantErr: uccalendar.ErrInvalidInput,
		},
		{
			name: "rejects non-string JSON value on locale option without panicking",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"options":[{"name":"set","type":1,"options":[{"name":"locale","type":3,"value":123}]}]
			}`,
			wantErr: uccalendar.ErrInvalidInput,
		},
		{
			name: "rejects wrong option type on locale option",
			payload: `{
				"id":"1","name":"uc-config","type":1,
				"options":[{"name":"set","type":1,"options":[{"name":"locale","type":6,"value":"en-US"}]}]
			}`,
			wantErr: uccalendar.ErrInvalidInput,
		},
	}

	for _, tc := range invalidConfigCases {
		t.Run(tc.name, func(t *testing.T) {
			data := mustUnmarshalSlashData(t, tc.payload)
			_, parseErr := uccmd.ParseConfigUpdateOptions(data)
			if !errors.Is(parseErr, tc.wantErr) {
				t.Errorf("ParseConfigUpdateOptions error = %v, want %v", parseErr, tc.wantErr)
			}
		})
	}

	badWindow := mustUnmarshalSlashData(t, `{
		"id":"1","name":"uc","type":1,
		"options":[{"name":"window","type":3,"value":42}]
	}`)
	if _, checkErr := uccmd.ParseCheckOptions("200000000000000001", badWindow); !errors.Is(checkErr, uccalendar.ErrInvalidInput) {
		t.Errorf("ParseCheckOptions(non-string window) = %v, want ErrInvalidInput", checkErr)
	}

	badClear := mustUnmarshalSlashData(t, `{
		"id":"1","name":"uc-config","type":1,
		"options":[{"name":"clear","type":1,"options":[{"name":"target","type":3,"value":false}]}]
	}`)
	if _, clearErr := uccmd.ParseClearTargetOption(badClear); !errors.Is(clearErr, uccalendar.ErrInvalidInput) {
		t.Errorf("ParseClearTargetOption(non-string target) = %v, want ErrInvalidInput", clearErr)
	}
}

func FuzzParseSlashOptions(f *testing.F) {
	seeds := []string{
		`{"id":"1","name":"uc","type":1,"options":[{"name":"window","type":3,"value":"today"}]}`,
		`{"id":"1","name":"uc","type":1,"options":[{"name":"date","type":3,"value":"07/11"}]}`,
		`{"id":"1","name":"uc","type":1,"options":[{"name":"window","type":3,"value":99},{"name":"date","type":3,"value":true}]}`,
		`{"id":"1","name":"uc-config","type":1,"options":[{"name":"set","type":1,"options":[{"name":"friend","type":6,"value":"@everyone"}]}]}`,
		`{"id":"1","name":"uc-config","type":1,"options":[{"name":"set","type":1,"options":[{"name":"channel","type":7,"value":"0"}]}]}`,
		`{"id":"1","name":"uc-config","type":1,"options":[{"name":"clear","type":1,"options":[{"name":"target","type":3,"value":"friend"}]}]}`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		var data discord.SlashCommandInteractionData
		if err := json.Unmarshal(raw, &data); err != nil {
			return
		}

		query, checkErr := uccmd.ParseCheckOptions("200000000000000001", data)
		if checkErr == nil && query.Window != "" && query.Date != "" {
			t.Fatalf("ParseCheckOptions accepted both window=%q and date=%q", query.Window, query.Date)
		}

		update, updateErr := uccmd.ParseConfigUpdateOptions(data)
		if updateErr == nil {
			if update.FriendUserID != nil {
				if err := uccalendar.ValidateSnowflake(*update.FriendUserID); err != nil {
					t.Fatalf("ParseConfigUpdateOptions accepted invalid friend snowflake: %v", err)
				}
			}
			if update.ChannelID != nil {
				if err := uccalendar.ValidateSnowflake(*update.ChannelID); err != nil {
					t.Fatalf("ParseConfigUpdateOptions accepted invalid channel snowflake: %v", err)
				}
			}
			if update.Locale != nil {
				if _, err := uccalendar.ParseGuildLocale(*update.Locale); err != nil {
					t.Fatalf("ParseConfigUpdateOptions accepted invalid locale: %v", err)
				}
			}
		}

		_, clearErr := uccmd.ParseClearTargetOption(data)
		if clearErr != nil && !errors.Is(clearErr, uccalendar.ErrInvalidInput) {
			t.Fatalf("ParseClearTargetOption returned unexpected error %v", clearErr)
		}
	})
}
