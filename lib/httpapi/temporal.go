package httpapi

import (
	"context"
	"fmt"

	"github.com/coder/agentapi/lib/temporalconfig"
	"github.com/danielgtaylor/huma/v2"
)

type TemporalConfigResponse struct {
	Body struct {
		Config temporalconfig.Config `json:"config"`
		Path   string                `json:"path"`
	}
}
type TemporalConfigRequest struct{ Body temporalconfig.Document }
type TemporalProfilesResponse struct {
	Body struct {
		Profiles map[string]temporalconfig.Config `json:"profiles"`
		Path     string                           `json:"path"`
	}
}
type TemporalProfileRequest struct {
	Name string `path:"name"`
	Body temporalconfig.Document
}
type TemporalProfileNameRequest struct {
	Name string `path:"name"`
}
type TemporalProfilesImportRequest struct {
	Body temporalconfig.ProfilesDocument
}

func temporalResponse(store temporalconfig.Store, config temporalconfig.Config) *TemporalConfigResponse {
	response := &TemporalConfigResponse{}
	response.Body.Config, response.Body.Path = config, store.Path()
	return response
}
func temporalProfilesResponse(store temporalconfig.Store, profiles map[string]temporalconfig.Config) *TemporalProfilesResponse {
	response := &TemporalProfilesResponse{}
	response.Body.Profiles, response.Body.Path = profiles, store.ProfilesPath()
	return response
}

func (s *Server) getTemporalConfig(_ context.Context, _ *struct{}) (*TemporalConfigResponse, error) {
	s.temporalMu.Lock()
	defer s.temporalMu.Unlock()
	store, err := temporalconfig.NewStore(s.cwd)
	if err != nil {
		return nil, err
	}
	config, err := store.Read()
	if err != nil {
		return nil, fmt.Errorf("read Temporal settings: %w", err)
	}
	return temporalResponse(store, config), nil
}

func (s *Server) updateTemporalConfig(_ context.Context, input *TemporalConfigRequest) (*TemporalConfigResponse, error) {
	if err := input.Body.Config.Validate(); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	s.temporalMu.Lock()
	defer s.temporalMu.Unlock()
	store, err := temporalconfig.NewStore(s.cwd)
	if err != nil {
		return nil, err
	}
	if err := store.Write(input.Body.Config); err != nil {
		return nil, err
	}
	return temporalResponse(store, input.Body.Config), nil
}

func (s *Server) getTemporalProfiles(_ context.Context, _ *struct{}) (*TemporalProfilesResponse, error) {
	s.temporalMu.Lock()
	defer s.temporalMu.Unlock()
	store, err := temporalconfig.NewStore(s.cwd)
	if err != nil {
		return nil, err
	}
	profiles, err := store.ReadProfiles()
	if err != nil {
		return nil, err
	}
	return temporalProfilesResponse(store, profiles), nil
}

func (s *Server) putTemporalProfile(_ context.Context, input *TemporalProfileRequest) (*TemporalProfilesResponse, error) {
	return s.mergeTemporalProfiles(map[string]temporalconfig.Config{input.Name: input.Body.Config})
}

func (s *Server) importTemporalProfiles(_ context.Context, input *TemporalProfilesImportRequest) (*TemporalProfilesResponse, error) {
	if input.Body.Profiles == nil {
		return nil, huma.Error400BadRequest("profiles must be an object")
	}
	return s.mergeTemporalProfiles(input.Body.Profiles)
}

func (s *Server) mergeTemporalProfiles(incoming map[string]temporalconfig.Config) (*TemporalProfilesResponse, error) {
	for name, config := range incoming {
		if !temporalconfig.ValidProfileName(name) {
			return nil, huma.Error400BadRequest("invalid Temporal profile name")
		}
		if err := config.Validate(); err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
	}
	s.temporalMu.Lock()
	defer s.temporalMu.Unlock()
	store, err := temporalconfig.NewStore(s.cwd)
	if err != nil {
		return nil, err
	}
	profiles, err := store.ReadProfiles()
	if err != nil {
		return nil, err
	}
	for name, config := range incoming {
		profiles[name] = config
	}
	if err := store.WriteProfiles(profiles); err != nil {
		return nil, err
	}
	return temporalProfilesResponse(store, profiles), nil
}

func (s *Server) deleteTemporalProfile(_ context.Context, input *TemporalProfileNameRequest) (*TemporalProfilesResponse, error) {
	s.temporalMu.Lock()
	defer s.temporalMu.Unlock()
	store, err := temporalconfig.NewStore(s.cwd)
	if err != nil {
		return nil, err
	}
	profiles, err := store.ReadProfiles()
	if err != nil {
		return nil, err
	}
	if _, exists := profiles[input.Name]; !exists {
		return nil, huma.Error404NotFound("Temporal profile not found")
	}
	delete(profiles, input.Name)
	if err := store.WriteProfiles(profiles); err != nil {
		return nil, err
	}
	return temporalProfilesResponse(store, profiles), nil
}

func (s *Server) applyTemporalProfile(_ context.Context, input *TemporalProfileNameRequest) (*TemporalConfigResponse, error) {
	s.temporalMu.Lock()
	defer s.temporalMu.Unlock()
	store, err := temporalconfig.NewStore(s.cwd)
	if err != nil {
		return nil, err
	}
	profiles, err := store.ReadProfiles()
	if err != nil {
		return nil, err
	}
	config, exists := profiles[input.Name]
	if !exists {
		return nil, huma.Error404NotFound("Temporal profile not found")
	}
	if err := store.Write(config); err != nil {
		return nil, err
	}
	return temporalResponse(store, config), nil
}

func (s *Server) registerTemporalRoutes() {
	configure := func(o *huma.Operation) { o.Tags = []string{"Temporal"} }
	huma.Get(s.api, "/temporal", s.getTemporalConfig, configure)
	huma.Put(s.api, "/temporal", s.updateTemporalConfig, configure)
	huma.Get(s.api, "/temporal/profiles", s.getTemporalProfiles, configure)
	huma.Put(s.api, "/temporal/profiles", s.importTemporalProfiles, configure)
	huma.Put(s.api, "/temporal/profiles/{name}", s.putTemporalProfile, configure)
	huma.Delete(s.api, "/temporal/profiles/{name}", s.deleteTemporalProfile, configure)
	huma.Post(s.api, "/temporal/profiles/{name}/apply", s.applyTemporalProfile, configure)
}
