locals {
  prefix = "${var.context.tenant}-${var.context.region_code}"
  name   = "${local.prefix}-sqs-${var.context.stage}-orders"
}

# Built-in state stores model apply-time outputs without providers, provisioners, or cloud access.
resource "terraform_data" "this" {
  input = {
    queue = {
      arn        = "arn:aws:sqs:${var.context.region}:${var.context.account_id}:${local.name}"
      url        = "https://sqs.${var.context.region}.amazonaws.com/${var.context.account_id}/${local.name}"
      account_id = var.context.account_id
    }
    dead_letter_queue = "${local.name}-dlq"
    max_receive_count = 5
    tags              = merge(var.context.tags, { Name = local.name, Attributes = "orders" })
  }

}
