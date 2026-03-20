package main

import (
	"flag"
	"os"

	app "kagent/services/account/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18083", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	accountApp, err := app.New(app.Config{
		Addr:           *addr,
		HubRegisterURL: *hubRegisterURL,
		InstanceID:     *instanceID,
	})
	if err != nil {
		app.Errorf("%v", err)
		os.Exit(1)
	}
	if err := accountApp.Run(); err != nil {
		app.Errorf("%v", err)
		os.Exit(1)
	}
}
