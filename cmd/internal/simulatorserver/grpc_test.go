package simulatorserver

import (
	"context"
	"testing"

	simulatorv1 "github.com/k-kohey/axe/pkg/simulatorapi/v1"
)

func TestGRPCServerListDevices(t *testing.T) {
	server := NewGRPCServer(newFakeManager(), "test")

	resp, err := server.ListDevices(context.Background(), &simulatorv1.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.GetDeviceTypes()) != 1 {
		t.Fatalf("device types = %d", len(resp.GetDeviceTypes()))
	}
	device := resp.GetDeviceTypes()[0]
	if device.GetName() != "iPhone 16 Pro" {
		t.Fatalf("device = %+v", device)
	}
	if len(device.GetRuntimes()) != 1 || device.GetRuntimes()[0].GetName() != "iOS 18.2" {
		t.Fatalf("runtimes = %+v", device.GetRuntimes())
	}
}

func TestGRPCServerSendInput(t *testing.T) {
	manager := newFakeManager()
	server := NewGRPCServer(manager, "test")

	_, err := server.SendInput(context.Background(), &simulatorv1.SendInputRequest{
		SessionId: "session-1",
		Input: &simulatorv1.InputEvent{
			Event: &simulatorv1.InputEvent_Text{Text: &simulatorv1.TextEvent{Value: "a"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.inputs) != 1 || manager.inputs[0].Text != "a" {
		t.Fatalf("inputs = %+v", manager.inputs)
	}
}
