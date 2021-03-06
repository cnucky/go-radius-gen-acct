package main

import (
	"context"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/routecall/go-radius-gen-acct/cdr"
	"github.com/routecall/go-radius-gen-acct/rfc2866"
	daemon "github.com/sevlyar/go-daemon"
	"github.com/urfave/cli"
	"go.uber.org/ratelimit"
	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
)

const Version = "0.12.3"

// max int value
const MaxUint = ^uint(0)
const MaxInt = int(MaxUint >> 1)

// config struct with all user options
type Config struct {
	NASPort      int
	NASIPAddress string
	Server       string
	Port         string
	Key          string
	PPS          int
	MaxReq       int
	ShowCount    bool
	Daemon       bool
	LogFileName  string
	PidFileName  string
	Retry        int
	MaxRetry     int
	CustomFields string
}

// used for --custom-fields
type CustomFields struct {
	ID    radius.Type
	Value string
}
type MapCustomFields map[int]CustomFields

func NewMapCustomFields() MapCustomFields {
	return make(MapCustomFields)
}

// parse struct CdrValues to radius packet
func ParseCdrAttributes(p *radius.Packet, c *cdr.CdrValues, cfg Config) {
	rfc2866.SipAcctStatusType_Add(p, rfc2866.SipAcctStatusType_Value_Stop)
	rfc2866.SipServiceType_Add(p, rfc2866.SipServiceType_Value_SipSession)
	rfc2866.SipResponseCode_AddString(p, c.ResponseCode)
	rfc2866.SipMethod_Add(p, rfc2866.SipMethod_Value_INVITE)
	rfc2866.SipEventTimestamp_Add(p, c.EventTimestamp)
	rfc2866.SipFromTag_AddString(p, c.FromTag)
	rfc2866.SipToTag_AddString(p, c.ToTag)
	rfc2866.SipCallerID_AddString(p, c.CallerId)
	rfc2866.SipCalleeID_AddString(p, c.CalleeId)
	rfc2866.SipDstNumber_AddString(p, c.DstNumber)
	rfc2866.SipAcctSessionID_AddString(p, c.AcctSessionId)
	rfc2866.SipCallMSDuration_Add(p, rfc2866.SipCallMSDuration(c.MsDuration))
	rfc2866.SipCallSetuptime_Add(p, rfc2866.SipCallSetuptime(c.SetupTime))
	rfc2865.NASPort_Add(p, rfc2865.NASPort(cfg.NASPort))
	rfc2865.NASIPAddress_Add(p, net.ParseIP(cfg.NASIPAddress))
	return
}

// send the radius Accounting-Request package to server
func SendAcct(c *cdr.CdrValues, mcf MapCustomFields, cfg Config) {
	client := radius.Client{
		Retry:           time.Second * time.Duration(cfg.Retry),
		MaxPacketErrors: cfg.MaxRetry,
	}
	packet := radius.New(radius.CodeAccountingRequest, []byte(cfg.Key))
	ParseCdrAttributes(packet, c, cfg)
	if mcf != nil {
		AddCustomField(packet, mcf)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Second * time.Duration(cfg.Retry*cfg.MaxRetry))
		cancel()
	}()

	_, err := client.Exchange(ctx, packet, cfg.Server+":"+cfg.Port)
	if err != nil {
		log.Fatal("error: ", err)
		os.Exit(1)
	}
}

// create and set the Config struct
func CliConfig() Config {
	cfg := Config{}
	cfg.CliCreate()

	return cfg
}

// cli - command-line
func (cfg *Config) CliCreate() {
	parsed := false
	app := cli.NewApp()
	app.Usage = "A Go (golang) RADIUS client accounting (RFC 2866) implementation for perfomance testing"
	app.UsageText = "go-radius-gen-acct - A Go (golang) RADIUS client accounting (RFC 2866) implementation for perfomance testing with generated data according dictionary (./dictionary.routecall.opensips) and RFC2866 (./rfc2866)."
	app.Version = Version
	app.Compiled = time.Now()

	app.Flags = []cli.Flag{
		cli.IntFlag{
			Name:        "pps, p",
			Value:       10,
			Usage:       "packets per second",
			Destination: &cfg.PPS,
		},
		cli.StringFlag{
			Name:        "server, s",
			Usage:       "server to send accts",
			Destination: &cfg.Server,
		},
		cli.StringFlag{
			Name:        "port, P",
			Value:       "1813",
			Usage:       "port to send accts",
			Destination: &cfg.Port,
		},
		cli.StringFlag{
			Name:        "nas-ip",
			Value:       "127.0.0.1",
			Usage:       "NAS-IP-Address on radius packet",
			Destination: &cfg.NASIPAddress,
		},
		cli.IntFlag{
			Name:        "nas-port",
			Value:       5666,
			Usage:       "NAS-Port on radius packet",
			Destination: &cfg.NASPort,
		},
		cli.StringFlag{
			Name:        "key, k",
			Usage:       "key for acct",
			Destination: &cfg.Key,
		},
		cli.IntFlag{
			Name:        "max-req, m",
			Value:       MaxInt,
			Usage:       "stop the test and exit when max-req are reached",
			Destination: &cfg.MaxReq,
		},
		cli.IntFlag{
			Name:        "retry-int, r",
			Value:       3,
			Usage:       "interval in second, on which to resend packet (zero or negative value means no retry)",
			Destination: &cfg.Retry,
		},
		cli.IntFlag{
			Name:        "max-retry",
			Value:       20,
			Usage:       "max retrys before exit the program",
			Destination: &cfg.MaxRetry,
		},
		cli.BoolFlag{
			Name:  "stats, c",
			Usage: "show count of requests",
		},
		cli.BoolFlag{
			Name:  "daemon, d",
			Usage: "daemon (background) proccess",
		},
		cli.StringFlag{
			Name:        "log-file",
			Value:       "./go-radius-gen-acct.log",
			Usage:       "the destination file of the log",
			Destination: &cfg.LogFileName,
		},
		cli.StringFlag{
			Name:        "pid-file",
			Value:       "./go-radius-gen-acct.pid",
			Usage:       "file to save the pid of daemon",
			Destination: &cfg.PidFileName,
		},
		cli.StringFlag{
			Name:        "custom-fields",
			Value:       "",
			Usage:       "--custom-fields \"ID=Value,ID=Value\"",
			Destination: &cfg.CustomFields,
		},
	}

	// options required
	app.Action = func(c *cli.Context) error {
		if cfg.PPS <= 0 {
			return cli.NewExitError("pps must be greater 0", 1)
		}
		if len(cfg.Server) <= 0 {
			return cli.NewExitError("server not defined", 1)
		}
		if len(cfg.Key) <= 0 {
			return cli.NewExitError("key not defined", 1)
		}
		if c.Bool("c") {
			cfg.ShowCount = true
		}
		if c.Bool("d") {
			cfg.Daemon = true
		}
		parsed = true
		return nil
	}

	err := app.Run(os.Args)
	if err != nil || parsed == false {
		os.Exit(1)
	}
}

func LogStats(wg *sync.WaitGroup, c Config, t *uint64) {
	defer wg.Done()
	for {
		countTotalS := atomic.LoadUint64(t)
		if countTotalS >= uint64(c.MaxReq) {
			break
		}
		time.Sleep(1000 * time.Millisecond)
		// -c count option
		// I hope the compiler solve this if
		if c.ShowCount {
			log.Print("")
			log.Print("Stats [refresh 1s]:")
			log.Print("estimated accounting-request per second:  ", atomic.LoadUint64(t)-countTotalS)
			log.Print("total count accounting-request:           ", atomic.LoadUint64(t))
		}
	}
}

func ParseCustomFields(c string) (MapCustomFields, error) {
	mapCustomFields := NewMapCustomFields()
	attrs := strings.Split(c, ",")
	for k, att := range attrs {
		s := strings.Split(att, "=")
		id, err := strconv.Atoi(s[0])
		if err != nil {
			return nil, err
		}
		mapCustomFields[k] = CustomFields{radius.Type(id), s[1]}
	}
	return mapCustomFields, nil
}

func AddCustomField(p *radius.Packet, mcf MapCustomFields) {
	for _, c := range mcf {
		p.Add(c.ID, []byte(c.Value))
	}
}

func GetMapCustomFields(c string) (MapCustomFields, error) {
	if len(c) > 0 {
		mapCustomFields, err := ParseCustomFields(c)
		if err != nil {
			return nil, err
		}
		return mapCustomFields, nil
	}
	return nil, nil
}

func main() {
	cfg := CliConfig()
	var countTotal uint64
	var wg sync.WaitGroup
	// set ratelimit
	rl := ratelimit.New(cfg.PPS)

	if cfg.Daemon {
		cntxt := &daemon.Context{
			PidFileName: cfg.PidFileName,
			PidFilePerm: 0644,
			LogFileName: cfg.LogFileName,
			LogFilePerm: 0640,
			WorkDir:     "./",
			Umask:       027,
		}
		d, err := cntxt.Reborn()
		if err != nil {
			log.Fatal("Unable to run: ", err)
		}
		if d != nil {
			return
		}
		defer cntxt.Release()
		log.Print("daemon started")
	}

	if cfg.ShowCount {
		wg.Add(1)
		go LogStats(&wg, cfg, &countTotal)
	}

	for i := 0; i < cfg.MaxReq; i++ {
		_ = rl.Take()
		wg.Add(1)
		go func() {
			defer wg.Done()
			atomic.AddUint64(&countTotal, 1)
			c := cdr.FillCdr()
			mapCustomFields, _ := GetMapCustomFields(cfg.CustomFields)
			SendAcct(c, mapCustomFields, cfg)
		}()
	}

	wg.Wait()
}
