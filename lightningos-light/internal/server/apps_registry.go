package server

import (
  "errors"
  "fmt"
)

func (s *Server) appRegistry() ([]appHandler, error) {
  apps := []appHandler{
    newBitcoinCoreApp(s),
    newLndgApp(s),
    newElementsApp(s),
  }
  if err := validateAppRegistry(apps); err != nil {
    return nil, err
  }
  return apps, nil
}

func (s *Server) appByID(id string) (appHandler, error) {
  apps, err := s.appRegistry()
  if err != nil {
    return nil, err
  }
  for _, app := range apps {
    if app.Definition().ID == id {
      return app, nil
    }
  }
  return nil, nil
}

func validateAppRegistry(apps []appHandler) error {
  ids := map[string]bool{}
  ports := map[int]bool{}
  for _, app := range apps {
    def := app.Definition()
    if def.ID == "" {
      return errors.New("app definition missing id")
    }
    if def.Name == "" {
      return fmt.Errorf("app %s missing name", def.ID)
    }
    if ids[def.ID] {
      return fmt.Errorf("duplicate app id: %s", def.ID)
    }
    ids[def.ID] = true
    if def.Port < 0 {
      return fmt.Errorf("app %s has invalid port %d", def.ID, def.Port)
    }
    if def.Port > 0 {
      if ports[def.Port] {
        return fmt.Errorf("duplicate app port: %d", def.Port)
      }
      ports[def.Port] = true
    }
  }
  return nil
}
