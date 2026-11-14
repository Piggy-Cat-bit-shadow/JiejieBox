package main

import (
	"context"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/spf13/cobra"
)

var commandCheck = &cobra.Command{
	Use:   "check",
	Short: "Check configuration",
	Run: func(cmd *cobra.Command, args []string) {
		err := check()
		if err != nil {
			log.Fatal(err)
		}
	},
	Args: cobra.NoArgs,
}

func init() {
	mainCommand.AddCommand(commandCheck)
}

func check() error {
	options, err := readConfigAndMerge()
	if err != nil {
		return err
	}
	return checkOptions(options)
}

// checkOptions constructs the configuration and prints the warnings construction emits.
//
// `check` never starts the service, and the log factory buffers every entry until Start and
// discards the buffer on Close. Construction-time warnings were therefore invisible to the
// command that exists to review configuration: check passed a configuration whose own
// constructs said something was wrong. Starting the factory here, and nothing else, flushes
// those entries without opening a listener, dialing an outbound or starting a background task.
//
// The level and destination are overridden for the review:
//
//   - warn, because lower levels are construction chatter and the operator needs the lines
//     that name an actionable configuration defect;
//   - stderr, because the command's output is what CI and deployment scripts read, not a
//     log file the running service may be configured to use.
//
// `log.disabled` is left alone: an explicit opt-out is not overridden by a validation
// command.
func checkOptions(options option.Options) error {
	if options.Log == nil {
		options.Log = &option.LogOptions{}
	}
	if disableColor {
		options.Log.DisableColor = true
	}
	if !options.Log.Disabled {
		options.Log.Level = "warn"
		options.Log.Output = "stderr"
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(globalCtx))
	defer cancel()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: options,
	})
	if err != nil {
		return err
	}
	logFactory := instance.LogFactory()
	if logFactory != nil {
		// The only failure Start can report is opening a log file, and the destination
		// is stderr, so there is nothing check could add here.
		_ = logFactory.Start()
	}
	_ = instance.Close()
	if logFactory != nil {
		_ = logFactory.Close()
	}
	return nil
}
