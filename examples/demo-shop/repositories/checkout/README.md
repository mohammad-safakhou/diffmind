# Checkout

Synthetic Spring checkout API. It calls Payment with OpenFeign and publishes an
`orders.created` Kafka event consumed by Notification.
