package security

// Permission 是管理面操作权限，命名遵循"资源:动作"。
type Permission string

// 管理面权限。
const (
	PermTenantsRead  Permission = "tenants:read"
	PermTenantsWrite Permission = "tenants:write"
	PermKeysRead     Permission = "keys:read"
	PermKeysWrite    Permission = "keys:write"
	PermTicketsRead  Permission = "tickets:read"
	PermTicketsWrite Permission = "tickets:write"
	PermNodesRead    Permission = "nodes:read"
	PermNodesWrite   Permission = "nodes:write"
)

// 内置角色。
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var allPermissions = []Permission{
	PermTenantsRead, PermTenantsWrite,
	PermKeysRead, PermKeysWrite,
	PermTicketsRead, PermTicketsWrite,
	PermNodesRead, PermNodesWrite,
}

// RBAC 维护角色到权限集的映射并做授权判定。
type RBAC struct {
	roles map[string]map[Permission]struct{}
}

// NewRBAC 构造内置 admin/operator/viewer 角色。
func NewRBAC() *RBAC {
	r := &RBAC{roles: map[string]map[Permission]struct{}{}}
	r.roles[RoleViewer] = permSet(PermTenantsRead, PermKeysRead, PermTicketsRead, PermNodesRead)
	r.roles[RoleOperator] = permSet(
		PermTenantsRead, PermKeysRead, PermTicketsRead, PermNodesRead,
		PermTicketsWrite, PermNodesWrite,
	)
	r.roles[RoleAdmin] = permSet(allPermissions...)
	return r
}

// Grant 给角色追加权限（角色不存在则创建）。
func (r *RBAC) Grant(role string, perms ...Permission) {
	s, ok := r.roles[role]
	if !ok {
		s = map[Permission]struct{}{}
		r.roles[role] = s
	}
	for _, p := range perms {
		s[p] = struct{}{}
	}
}

// Allowed 判断用户的任一角色是否拥有该权限。
func (r *RBAC) Allowed(roles []string, perm Permission) bool {
	for _, role := range roles {
		if s, ok := r.roles[role]; ok {
			if _, has := s[perm]; has {
				return true
			}
		}
	}
	return false
}

func permSet(perms ...Permission) map[Permission]struct{} {
	s := map[Permission]struct{}{}
	for _, p := range perms {
		s[p] = struct{}{}
	}
	return s
}
