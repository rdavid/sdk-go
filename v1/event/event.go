package event

import (
	"encoding/json"
	"fmt"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/redhat-cne/sdk-go/pkg/channel"
	"github.com/redhat-cne/sdk-go/pkg/pubsub"
	"log"

	"github.com/redhat-cne/sdk-go/pkg/event"
)

//PublishCloudEventToLog .. publish event data to a log
func PublishCloudEventToLog(e cloudevents.Event) {
	log.Printf("Publishing event to log %#v", e)

}

//CloudNativeEvent
func CloudNativeEvent() event.Event {
	return event.Event{}
}

//CloudNativeEventData
func CloudNativeData() event.Data {
	return event.Data{}
}

//CloudNativeEventDataValues
func CloudNativeDataValues() event.DataValue {
	return event.DataValue{}
}

//SendEventToLog ...
func SendEventToLog(e event.Event) {
	log.Printf("Publishing event to log %#v", e)
}

//SendNewEventToDataChannel send created publisher information for QDR to process
func SendNewEventToDataChannel(outChan chan<- channel.DataChan, address string, e *cloudevents.Event) {
	// go ahead and create QDR to this address
	outChan <- channel.DataChan{
		Address: address,
		Data:    e,
		Status:  channel.NEW,
		Type:    channel.EVENT,
	}
}

//SendStatusToDataChannel send created publisher information for QDR to process
func SendStatusToDataChannel(outChan chan<- channel.DataChan, status channel.Status, address string) {
	// go ahead and create QDR to this address
	outChan <- channel.DataChan{
		Address: address,
		Type:    channel.STATUS,
		Status:  status,
	}
}

// SendCloudEventsToDataChannel sends data event in cloudevents format to data channel
func SendCloudEventsToDataChannel(outChan chan<- channel.DataChan, status channel.Status, address string, e cloudevents.Event) {
	outChan <- channel.DataChan{
		Address: address,
		Data:    &e,
		Status:  status,
		Type:    channel.EVENT,
	}
}

//CreateCloudEvents create new cloud event from cloud native events and pubsub
func CreateCloudEvents(e event.Event, ps pubsub.PubSub) (*cloudevents.Event, error) {
	ce := cloudevents.NewEvent(cloudevents.VersionV03)
	ce.SetTime(e.GetTime())
	ce.SetType(e.Type)
	ce.SetDataContentType(cloudevents.ApplicationJSON)
	ce.SetSource(ps.Resource) // bus address
	ce.SetSpecVersion(cloudevents.VersionV03)
	ce.SetID(uuid.New().String())
	if err := ce.SetData(cloudevents.ApplicationJSON, e.GetData()); err != nil {
		return nil, err
	}
	return &ce, nil
}

// GetCloudNativeEvents  get event data from cloud events object if its valid else return error
func GetCloudNativeEvents(ce cloudevents.Event) (err error) {
	e := event.Event{}
	if ce.Data() == nil {
		return fmt.Errorf("event data is empty")
	}
	data := event.Data{}
	if err = json.Unmarshal(ce.Data(), &data); err != nil {
		return
	}
	e.SetDataContentType(event.ApplicationJSON)
	e.SetTime(ce.Time())
	e.SetType(ce.Type())
	e.SetData(data)
	return
}
