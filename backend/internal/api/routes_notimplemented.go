package api

import "cloudsim/backend/internal/apierr"

// Services shown in the console as "coming later". Every method and sub-path answers
// 501 NotImplemented (after authentication) so clients get an explicit, documented error.
var notImplementedServices = []struct{ prefix, name string }{
	{"lambda", "Lambda"}, {"rds", "RDS"}, {"ecs", "ECS"}, {"ecr", "ECR"}, {"eks", "EKS"}, {"cloudformation", "CloudFormation"},
}

func init() {
	RegisterModule("notimplemented", 99, func(s *Server) {
		for _, svc := range notImplementedServices {
			name := svc.name
			h := func(c *Ctx) error {
				return apierr.NotImplemented("%s is not implemented in this release of CloudSim (see docs/services.md for scope and roadmap)", name)
			}
			s.op("/api/v1/"+svc.prefix, "", nil, h)
			s.op("/api/v1/"+svc.prefix+"/", "", nil, h)
		}
	})
}
