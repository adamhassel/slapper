# slapper

__Simple load testing tool with real-time updated histogram of request timings__

![slapper](https://raw.githubusercontent.com/ikruglov/slapper/master/img/example.gif)

## Interface

![interface](https://raw.githubusercontent.com/ikruglov/slapper/master/img/interface.png)

## Usage
```bash
$ ./slapper -help
Usage of ./slapper:
  -H value
    	HTTP header 'key: value' set on all requests. Repeat for more than one header.
  -base64body
    	Bodies in targets file are base64-encoded
  -maxY duration
    	max on Y axe (default 100ms)
  -minY duration
    	min on Y axe (default 0ms)
  -rate uint
    	Requests per second (default 50)
  -targets string
    	Targets file
  -timeout duration
    	Requests timeout (default 30s)
  -workers uint
    	Number of workers (default 8)

```

## Key bindings
* q, ctrl-c - quit
* r - reset stats
* k - increase rate by 100 RPS
* j - decrease rate by 100 RPS

## Targets syntax

The targets file is line-based. Its syntax is:

	HTTP_METHOD url
	$ body

The body line is optional. The rules for what is considered to be a body
line are:

1. If something starts with `$ ` (dollar-sign and space), it's a body
2. If the line is literally `{}`, it's an empty body

A missing body line is taken to mean an empty request body. Point (2) is there
for backwards-compatibility.

### Randomizing traffic
(WIP)

For hitting many different URLs without having to put them all in a file,
slapper supports a randomizing and a range syntax in the url part:

* [\<start\>;\<end\>], for example `https://www.example.com/[100;900]/foo` will have slapper visit `example.com/100/foo` through `example.com/900/foo`
* [r\<length\>;\<alphabet\>], will generate random character sequences of `length` using characters in `alphabet`. `alphabet` is ranges of characters, separated by `_`, for example `a-z_0-9` (Note: at this point, only an alphabet consisting of a single range is supported, e.g. `[a-z]`)
* If you use range with random, the range determines the number of unique URLs generated. If you only use randomness, put an integer after the URL to determine the number of unique URLs generated. E.g. `http://example.com/[r10;a-z] 10` will generate 10 urls with random strings. 


## Acknowledgement
* Idea and initial implementation is by @sparky
* This module was originally developed for Booking.com.
* Forked from  github.com/ikruglov/slapper
