package plan

func staticPlan(root string) (Plan, error) {
	return Plan{
		Provider:     "static",
		Language:     "Static site",
		BuilderImage: "nginxinc/nginx-unprivileged:alpine",
		RuntimeImage: "nginxinc/nginx-unprivileged:alpine",
		RuntimeKind:  "static",
		StartCommand: []string{"nginx", "-g", "daemon off;"},
		Port:         8080,
	}, nil
}
