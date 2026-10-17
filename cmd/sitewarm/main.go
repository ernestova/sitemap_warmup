// Command sitewarm warms the top pages of a site through its CDN after a
// deploy and verifies that each one is served from cache.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/clarivo-co/sitewarm/internal/report"
	"github.com/clarivo-co/sitewarm/pkg/sitewarm"
	"github.com/spf13/cobra"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitSIGINT  = 130
	exitSIGTERM = 143
)

func main() {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	os.Exit(run(context.Background(), os.Args[1:], env{
		stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv,
		signals: sigCh, exit: os.Exit,
	}))
}

// env is everything run touches outside its arguments, so tests can
// inject signals and capture the exit of a forced stop.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	signals        <-chan os.Signal
	exit           func(int)
}

func signalCode(s os.Signal) int {
	if s == syscall.SIGTERM {
		return exitSIGTERM
	}
	return exitSIGINT
}

func signalName(s os.Signal) string {
	if s == syscall.SIGTERM {
		return "SIGTERM"
	}
	return "SIGINT"
}

func run(ctx context.Context, args []string, e env) int {
	code := exitOK
	root := &cobra.Command{
		Use:           "sitewarm",
		Short:         "Warm your top pages through the CDN after a deploy and verify they are cached",
		Version:       sitewarm.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(e.stdout)
	root.SetErr(e.stderr)
	root.SetArgs(args)
	root.AddCommand(scanCmd(ctx, e, &code))

	if err := root.Execute(); err != nil {
		fmt.Fprintln(e.stderr, "sitewarm:", err)
		return exitUsage
	}
	return code
}

func scanCmd(ctx context.Context, e env, code *int) *cobra.Command {
	opts := sitewarm.DefaultOptions()
	var format, reportPath string
	cmd := &cobra.Command{
		Use:   "scan (--top-from <file.csv> | --from-sitemap <url>) [flags]",
		Short: "Warm and verify every URL with the desktop and mobile profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if format != "table" && format != "json" && format != "csv" {
				return fmt.Errorf("--format must be table, json or csv, got %q", format)
			}
			if (opts.TopFrom == "") == (opts.FromSitemap == "") {
				return sitewarm.ErrSource
			}
			if opts.FromSitemap != "" && opts.IgnoreRobots {
				fmt.Fprintln(e.stderr, "warning: --ignore-robots set; robots.txt is not checked")
			}
			opts.Cookies = map[string]string{}
			for _, p := range opts.Profiles {
				if c := e.getenv("SITEWARM_COOKIE_" + strings.ToUpper(p)); c != "" {
					opts.Cookies[p] = c
				}
			}

			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			got := make(chan os.Signal, 1)
			stop := make(chan struct{})
			defer close(stop)
			go func() {
				select {
				case s := <-e.signals:
					got <- s
					fmt.Fprintf(e.stderr, "sitewarm: %s received, writing a partial report (again to force exit)\n", signalName(s))
					cancel()
				case <-stop:
					return
				}
				select {
				case s := <-e.signals:
					e.exit(signalCode(s))
				case <-stop:
				}
			}()

			res, err := sitewarm.Scan(ctx, opts)
			if err != nil {
				return err
			}
			var sig os.Signal
			select {
			case sig = <-got:
				res.Stopped = signalName(sig)
			default:
			}

			if err := writeReport(e.stdout, res, format); err != nil {
				return err
			}
			if reportPath != "" {
				if err := writeReportFile(reportPath, res); err != nil {
					return err
				}
			}
			switch {
			case sig != nil:
				*code = signalCode(sig)
			case !res.Summary.Passed:
				*code = exitFailed
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.TopFrom, "top-from", "", "CSV with a url (or path) column, in priority order")
	f.StringVar(&opts.FromSitemap, "from-sitemap", "", "sitemap or sitemap index URL (nested indexes and .gz are followed)")
	f.BoolVar(&opts.IgnoreRobots, "ignore-robots", false, "with --from-sitemap, do not check robots.txt")
	f.StringVar(&opts.BaseURL, "base-url", "", "base URL for a CSV path column")
	f.IntVar(&opts.Top, "top", opts.Top, "warm only the first N URLs")
	f.StringVar(&opts.AcceptLanguage, "accept-language", "", "Accept-Language for the browser presets")
	f.StringSliceVar(&opts.CookieHosts, "cookie-hosts", nil, "hosts allowed to receive SITEWARM_COOKIE_<PROFILE> cookies (HTTPS only)")
	f.IntVar(&opts.Concurrency, "concurrency", opts.Concurrency, "requests in flight at once")
	f.Float64Var(&opts.RPS, "rps", opts.RPS, "global requests per second, retries included")
	f.DurationVar(&opts.RetryInterval, "retry-interval", opts.RetryInterval, "wait between verification attempts")
	f.DurationVar(&opts.MaxWait, "max-wait", opts.MaxWait, "how long to keep retrying a miss or stale URL (0 = one request)")
	f.DurationVar(&opts.RunTimeout, "run-timeout", opts.RunTimeout, "cap for the whole run; unfinished URLs become incomplete")
	f.StringVar(&opts.FailOn, "fail-on", opts.FailOn, "states that make the run fail (comma-separated; empty = never)")
	f.StringVar(&format, "format", "table", "stdout format: table, json or csv (url,status,state)")
	f.StringVar(&reportPath, "report", "", "also write the JSON report to this file")
	return cmd
}

func writeReport(w io.Writer, res *sitewarm.Result, format string) error {
	switch format {
	case "json":
		return report.WriteJSON(w, res)
	case "csv":
		return report.WriteCSV(w, res)
	}
	return report.WriteTable(w, res)
}

func writeReportFile(path string, res *sitewarm.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := report.WriteJSON(f, res); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
