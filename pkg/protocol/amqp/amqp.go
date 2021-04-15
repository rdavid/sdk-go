package amqp

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Azure/go-amqp"
	amqp1 "github.com/cloudevents/sdk-go/protocol/amqp/v2"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	channel "github.com/redhat-cne/sdk-go/pkg/channel"
	"github.com/redhat-cne/sdk-go/pkg/protocol"
)

/*var (
	_ protocol.Protocol = (*Router)(nil)
)*/
var (
	amqpLinkCredit uint32 = 50
	cancelTimeout         = 2 * time.Second
)

//Protocol ...
type Protocol struct {
	protocol.Binder
	Protocol *amqp1.Protocol
}

//Router defines QDR router object
type Router struct {
	Listeners map[string]*Protocol
	Senders   map[string]*Protocol
	Host      string
	DataIn    <-chan *channel.DataChan
	DataOut   chan<- *channel.DataChan
	Client    *amqp.Client
	//close on true
	Close <-chan bool
}

//InitServer initialize QDR configurations
func InitServer(amqpHost string, dataIn <-chan *channel.DataChan, dataOut chan<- *channel.DataChan, closeCh <-chan bool) (*Router, error) {
	server := Router{
		Listeners: map[string]*Protocol{},
		Senders:   map[string]*Protocol{},
		DataIn:    dataIn,
		Host:      amqpHost,
		DataOut:   dataOut,
		Close:     closeCh,
	}
	client, err := server.NewClient(amqpHost, []amqp.ConnOption{})
	if err != nil {
		return nil, err
	}
	server.Client = client
	return &server, nil
}

// NewClient ...
func (q *Router) NewClient(server string, connOption []amqp.ConnOption) (*amqp.Client, error) {
	client, err := amqp.Dial(server, connOption...)
	if err != nil {
		return nil, err
	}
	return client, nil
}

//NewSession Open a session
func (q *Router) NewSession(sessionOption []amqp.SessionOption) (*amqp.Session, error) {
	session, err := q.Client.NewSession(sessionOption...)
	if err != nil {
		return session, err
	}
	return session, nil
}

//NewSender creates new QDR ptp
func (q *Router) NewSender(address string) error {
	var opts []amqp1.Option
	//p, err := amqp1.NewSenderProtocol(q.Host, address, []amqp.ConnOption{}, []amqp.SessionOption{}, opts...)
	session, err := q.NewSession([]amqp.SessionOption{})
	if err != nil {
		log.Printf("failed to create an amqp session for a sender : %v", err)
		return err
	}
	p, err := amqp1.NewSenderProtocolFromClient(q.Client, session, address, opts...)
	if err != nil {
		log.Printf("failed to create an amqp sender protocol: %v", err)
		return err
	}

	l := Protocol{}
	c, err := cloudevents.NewClient(p)
	if err != nil {
		log.Fatalf("failed to create an amqp sender client: %v", err)
	}
	l.Protocol = p
	l.Client = c
	q.Senders[address] = &l

	return nil
}

//NewReceiver creates new QDR receiver
func (q *Router) NewReceiver(address string) error {
	var opts []amqp1.Option
	opts = append(opts, amqp1.WithReceiverLinkOption(amqp.LinkCredit(amqpLinkCredit)))

	session, err := q.NewSession([]amqp.SessionOption{})

	if err != nil {
		log.Printf("failed to create an amqp session for a sender : %v", err)
		return err
	}

	p, err := amqp1.NewReceiverProtocolFromClient(q.Client, session, address, opts...)
	if err != nil {
		log.Printf("failed to create an amqp protocol for a receiver: %v", err)
		return err
	}
	log.Printf("(new receiver) router connection established %s\n", address)

	l := Protocol{}
	parent, cancelParent := context.WithCancel(context.TODO())
	l.CancelFn = cancelParent
	l.ParentContext = parent
	c, err := cloudevents.NewClient(p)
	if err != nil {
		log.Fatalf("failed to create a receiver client: %v", err)
	}
	l.Protocol = p
	l.Client = c
	q.Listeners[address] = &l
	return nil
}

//Receive is a QDR receiver listening to a address specified
func (q *Router) Receive(wg *sync.WaitGroup, address string, fn func(e cloudevents.Event)) {
	var err error
	defer wg.Done()
	if val, ok := q.Listeners[address]; ok {
		log.Printf("waiting and listening at  %s\n", address)
		err = val.Client.StartReceiver(val.ParentContext, fn)
		if err != nil {
			log.Printf("amqp receiver error: %v", err)
		}
	} else {
		log.Printf("amqp receiver not found in the list\n")
	}
}

//ReceiveAll creates receiver to all address and receives events for all address
func (q *Router) ReceiveAll(wg *sync.WaitGroup, fn func(e cloudevents.Event)) {
	defer wg.Done()
	var err error
	for _, l := range q.Listeners {
		wg.Add(1)
		go func(l *Protocol, wg *sync.WaitGroup) {
			fmt.Printf("listenining to queue %s by %s\n", l.Address, l.ID)
			defer wg.Done()
			err = l.Client.StartReceiver(context.Background(), fn)
			if err != nil {
				log.Printf("amqp receiver error: %v", err)
			}
		}(l, wg)
	}

}

//QDRRouter the QDR Server listens  on data and do either create sender or receivers
//QDRRouter is qpid router object configured to create publishers and  consumers
/*
//create a  status listener
in <- &channel.DataChan{
	Address: addr,
	Type:    channel.STATUS,
	Status:  channel.NEW,
    OnReceiveOverrideFn: func(e cloudevents.Event) error {}
    ProcessOutChDataFn: func (e event.Event) error {}

}
//create a sender
in <- &channel.DataChan{
	Address: addr,
	Type:    channel.SENDER,
}

// create a listener
in <- &channel.DataChan{
	Address: addr,
	Type:    channel.LISTENER,
}

// send data
in <- &channel.DataChan{
	Address: addr,
	Data:    &event,
	Status:  channel.NEW,
	Type:    channel.EVENT,
}
*/
func (q *Router) QDRRouter(wg *sync.WaitGroup) {
	wg.Add(1)
	go func(q *Router, wg *sync.WaitGroup) {
		defer wg.Done()
		for { //nolint:gosimple
			select {
			case d := <-q.DataIn:
				if d.Type == channel.LISTENER {
					// create receiver and let it run
					if d.Status == channel.DELETE {
						if listener, ok := q.Listeners[d.Address]; ok {
							listener.CancelFn()
							delete(q.Listeners, d.Address)
							log.Printf("listener deleted")
						}
					} else {
						if _, ok := q.Listeners[d.Address]; !ok {
							log.Printf("(1)listener not found for the following address %s, creating listener", d.Address)
							err := q.NewReceiver(d.Address)
							if err != nil {
								log.Printf("error creating Receiver %v", err)
							} else {
								wg.Add(1)
								go q.Receive(wg, d.Address, func(e cloudevents.Event) {
									out := channel.DataChan{
										Address:        d.Address,
										Data:           &e,
										Status:         channel.NEW,
										Type:           channel.EVENT,
										ProcessEventFn: d.ProcessEventFn,
									}
									if d.OnReceiveOverrideFn != nil {
										if err := d.OnReceiveOverrideFn(e); err != nil {
											out.Status = channel.FAILED
										} else {
											out.Status = channel.SUCCEED
										}
									}
									q.DataOut <- &out
								})
								log.Printf("done setting up receiver for consumer")
							}
						} else {
							log.Printf("(1)listener already found so not creating again %s\n", d.Address)
						}
					}
				} else if d.Type == channel.SENDER {
					//log.Printf("reading from data %s", d.Address)
					if d.Status == channel.DELETE {
						if sender, ok := q.Senders[d.Address]; ok {
							sender.Protocol.Close(context.Background())
							delete(q.Senders, d.Address)
							log.Printf("sender deleted")
						}
					} else {
						if _, ok := q.Senders[d.Address]; !ok {
							log.Printf("(1)sender not found for the following address, %s  will attempt to create", d.Address)
							err := q.NewSender(d.Address)
							if err != nil {
								log.Printf("(1)error creating sender %v for address %s", err, d.Address)
							}
						} else {
							log.Printf("(1)sender already found so not creating again %s\n", d.Address)
						}
					}
				} else if d.Type == channel.EVENT && d.Status == channel.NEW {
					if _, ok := q.Senders[d.Address]; ok {
						q.SendTo(wg, d.Address, d.Data)
					} else {
						log.Printf("did not find sender for address %s, will not try to create.", d.Address)
						log.Printf("store %#v", q.Senders)
					}
				}
			case <-q.Close:
				log.Printf("Received close order")
				for key, s := range q.Senders {
					_ = s.Protocol.Close(context.Background())
					delete(q.Senders, key)
				}
				for key, l := range q.Listeners {
					l.CancelFn()
					delete(q.Listeners, key)
				}
				return
			}
		}
	}(q, wg)
}

//SendTo sends events to the address specified
func (q *Router) SendTo(wg *sync.WaitGroup, address string, event *cloudevents.Event) {
	if sender, ok := q.Senders[address]; ok {
		wg.Add(1) //for each ptp you send a message since its
		go func(sender *Protocol, e *cloudevents.Event, wg *sync.WaitGroup) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
			defer cancel()
			if result := sender.Client.Send(ctx, *event); cloudevents.IsUndelivered(result) {
				log.Printf("Failed to send(TO): %s result %v, reason: no listeners", address, result)
				q.DataOut <- &channel.DataChan{
					Address: address,
					Data:    e,
					Status:  channel.FAILED,
					Type:    channel.EVENT,
				}
			} else if cloudevents.IsNACK(result) {
				log.Printf("Event not accepted: %v", result)
				q.DataOut <- &channel.DataChan{
					Address: address,
					Data:    e,
					Status:  channel.SUCCEED,
					Type:    channel.EVENT,
				}
			}
		}(sender, event, wg)
	}
}

// SendToAll ... sends events to all registered amqp address
func (q *Router) SendToAll(wg *sync.WaitGroup, event cloudevents.Event) {
	for k, s := range q.Senders {
		wg.Add(1) //for each ptp you send a message since its
		go func(s *Protocol, address string, e cloudevents.Event, wg *sync.WaitGroup) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1)*time.Second)
			defer cancel()
			if result := s.Client.Send(ctx, event); cloudevents.IsUndelivered(result) {
				log.Printf("Failed to send(TOALL): %v", result)
				q.DataOut <- &channel.DataChan{
					Address: address,
					Data:    &e,
					Status:  channel.FAILED,
					Type:    channel.EVENT,
				} // Not the clean way of doing , revisit
			} else if cloudevents.IsNACK(result) {
				log.Printf("Event not accepted: %v", result)
				q.DataOut <- &channel.DataChan{
					Address: address,
					Data:    &e,
					Status:  channel.SUCCEED,
					Type:    channel.EVENT,
				} // Not the clean way of doing , revisit
			}
		}(s, k, event, wg)
	}
}

// NewSenderReceiver created New Sender and Receiver object
func NewSenderReceiver(hostName string, port int, senderAddress, receiverAddress string) (sender, receiver *Protocol, err error) {
	sender, err = NewReceiver(hostName, port, senderAddress)
	if err == nil {
		receiver, err = NewSender(hostName, port, receiverAddress)
	}
	return
}

//NewReceiver creates new receiver object
func NewReceiver(hostName string, port int, receiverAddress string) (receiver *Protocol, err error) {
	receiver = &Protocol{}
	var opts []amqp1.Option
	opts = append(opts, amqp1.WithReceiverLinkOption(amqp.LinkCredit(amqpLinkCredit)))

	p, err := amqp1.NewReceiverProtocol(fmt.Sprintf("%s:%d", hostName, port), receiverAddress, []amqp.ConnOption{}, []amqp.SessionOption{}, opts...)
	if err != nil {
		log.Printf("Failed to create amqp protocol for a Receiver: %v", err)
		return
	}
	log.Printf("(New Receiver) Connection established %s\n", receiverAddress)

	parent, cancelParent := context.WithCancel(context.Background())
	receiver.CancelFn = cancelParent
	receiver.ParentContext = parent
	c, err := cloudevents.NewClient(p)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	receiver.Protocol = p
	receiver.Client = c
	return
}

//NewSender creates new QDR ptp
func NewSender(hostName string, port int, address string) (sender *Protocol, err error) {
	sender = &Protocol{}
	var opts []amqp1.Option
	p, err := amqp1.NewSenderProtocol(fmt.Sprintf("%s:%d", hostName, port), address, []amqp.ConnOption{}, []amqp.SessionOption{}, opts...)
	if err != nil {
		log.Printf("Failed to create amqp protocol: %v", err)
		return
	}
	c, err := cloudevents.NewClient(p)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	sender.Protocol = p
	sender.Client = c
	return
}
