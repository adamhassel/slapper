package main

import (
	"reflect"
	"regexp"
	"testing"
)

func Test_parseUrl(t *testing.T) {
	type args struct {
		url string
	}
	tests := []struct {
		name       string
		args       args
		want       []string
		wantlen    int
		wantre     *regexp.Regexp
		exactmatch bool
		wantErr    bool
	}{
		{
			name:       "no range/random, trivial case",
			args:       args{"http://www.example.com"},
			want:       []string{"http://www.example.com"},
			wantlen:    1,
			exactmatch: true,
			wantErr:    false,
		},
		{
			name:       "no range/random, invalid range specified",
			args:       args{"http://www.example.com 10"},
			want:       []string{"http://www.example.com"},
			wantlen:    1,
			exactmatch: true,
			wantErr:    false,
		},
		{
			name:       "random, single range",
			args:       args{"http://www.example.com/[r10;a-z] 10"},
			wantlen:    10,
			wantre:     regexp.MustCompile(`http://www.example.com/[a-z]{10}$`),
			exactmatch: false,
			wantErr:    false,
		},
		{
			name:       "random, dual range",
			args:       args{"http://www.example.com/[r10;a-z]/[r10;A-Z] 10"},
			wantlen:    10,
			wantre:     regexp.MustCompile(`http://www.example.com/[a-z]{10}/[A-Z]{10}$`),
			exactmatch: false,
			wantErr:    false,
		},
		{
			name:       "range",
			args:       args{"http://www.example.com/[100-900]"},
			wantlen:    801,
			wantre:     regexp.MustCompile(`http://www.example.com/[1-9][0-9]{2}$`),
			exactmatch: false,
			wantErr:    false,
		},
		{
			name:       "range with superflous count",
			args:       args{"http://www.example.com/[100-900] 100"},
			wantlen:    801,
			wantre:     regexp.MustCompile(`http://www.example.com/[1-9][0-9]{2}$`),
			exactmatch: false,
			wantErr:    false,
		},
		{
			name:       "randomness AND range",
			args:       args{"http://www.example.com/[r10;a-z]/[100-900]"},
			wantlen:    801,
			wantre:     regexp.MustCompile(`http://www.example.com/[a-z]{10}/[1-9][0-9]{2}$`),
			exactmatch: false,
			wantErr:    false,
		},
		{
			name:       "range AND randomness",
			args:       args{"http://www.example.com/[100-900]/[r10;a-z]"},
			wantlen:    801,
			wantre:     regexp.MustCompile(`http://www.example.com/[1-9][0-9]{2}/[a-z]{10}$`),
			exactmatch: false,
			wantErr:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUrl(tt.args.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseUrl() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantlen != len(got) {
				t.Errorf("parseUrl() got %d strings (%v), want %d", len(got), got, tt.wantlen)
			}
			if tt.wantre == nil && !tt.exactmatch {
				t.Errorf("parseUrl() test %s must have either exactmatch or wantre", tt.name)
			}
			if tt.wantre != nil {
				for _, s := range got {
					if !tt.wantre.MatchString(s) {
						t.Errorf("parseUrl() result (%v) didn't match expected format (%v)", s, tt.wantre.String())
					}
				}
			}
			if tt.exactmatch {
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("parseUrl() got = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func Test_makeCharList(t *testing.T) {
	type args struct {
		in charrange
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "a-z",
			args: args{in: charrange{min: 'a', max: 'z'}},
			want: "abcdefghijklmnopqrstuvwxyz",
		},
		{
			name: "0-9",
			args: args{in: charrange{min: '0', max: '9'}},
			want: "0123456789",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := makeCharList(tt.args.in); got != tt.want {
				t.Errorf("makeCharList() = %v, want %v", got, tt.want)
			}
		})
	}
}
