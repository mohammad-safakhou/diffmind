resource "aws_sqs_queue" "order_events" {
  name = "order-events"
  
  tags = {
    Service = "order-service"
    Team    = "commerce"
  }
}

resource "aws_sqs_queue" "payment_events" {
  name = "payment-events"
  
  tags = {
    Service = "billing-service"
    Team    = "payments"
  }
}
