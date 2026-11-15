// Package cameraadmin contributes only camera operator routes to go-admin.
package cameraadmin

import (
	"github.com/goliatone/go-admin/admin"
	"github.com/goliatone/go-admin/admin/routing"
	router "github.com/goliatone/go-router"
)

type CameraModule struct{ Diagnose, Reconnect router.HandlerFunc }

func (CameraModule) Manifest() admin.ModuleManifest {
	return admin.ModuleManifest{ID: "camera", NameKey: "Camera operations"}
}
func (CameraModule) RouteContract() routing.ModuleContract {
	return routing.ModuleContract{Slug: "camera", APIRouteDeclarations: map[string]routing.RouteDeclaration{
		"camera.diagnose":  {Method: router.POST, Path: "/cameras/:camera_id/diagnose"},
		"camera.reconnect": {Method: router.POST, Path: "/cameras/:camera_id/streams/:stream_id/reconnect"},
	}}
}
func (m CameraModule) Register(ctx admin.ModuleContext) error {
	ctx.ProtectedRouter.Post(ctx.Routing.RoutePath(routing.SurfaceAPI, "camera.diagnose"), m.Diagnose)
	ctx.ProtectedRouter.Post(ctx.Routing.RoutePath(routing.SurfaceAPI, "camera.reconnect"), m.Reconnect)
	return nil
}
