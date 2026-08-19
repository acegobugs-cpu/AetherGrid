output "node_count" {
  description = "Number of provisioned nodes"
  value       = var.node_count
}

output "node_ids" {
  description = "Unique identifiers of each provisioned node"
  value       = local_file.node[*].id
}

output "node_names" {
  description = "Human-readable names of each provisioned node"
  value       = [for i in range(var.node_count) : "${var.base_name}-${i + 1}"]
}

output "node_addresses" {
  description = "Network address of each provisioned node"
  value       = [for i in range(var.node_count) : "10.0.0.${i + 1}"]
}
