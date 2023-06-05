package provider

type DeliveryProvider interface {
	Delivery() error
	Uninstall() error
}
