package bootstrap

import (
	json2 "encoding/json"
	"log"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/integration-system/isp-kit/app"
	"github.com/integration-system/isp-kit/cluster"
	"github.com/integration-system/isp-kit/config"
	"github.com/integration-system/isp-kit/json"
	"github.com/integration-system/isp-kit/rc"
	"github.com/integration-system/isp-kit/rc/schema"
	"github.com/integration-system/isp-kit/validator"
	"github.com/pkg/errors"
)

type Bootstrap struct {
	App            *app.Application
	ClusterCli     *cluster.Client
	RemoteConfig   *rc.Config
	BindingAddress string
	MigrationsDir  string
	ModuleName     string
}

func New(moduleVersion string, remoteConfig interface{}, endpoints []cluster.EndpointDescriptor) *Bootstrap {
	isDev := strings.ToLower(os.Getenv("APP_MODE")) == "dev"
	localConfigPath, err := configFilePath(isDev)
	if err != nil {
		log.Fatal(errors.WithMessage(err, "resolve local config path"))
		return nil
	}
	app, err := app.New(
		isDev,
		config.WithValidator(validator.Default),
		config.WithReadingFromFile(localConfigPath),
	)
	if err != nil {
		log.Fatal(errors.WithMessage(err, "create app"))
		return nil
	}

	boot, err := bootstrap(isDev, app, moduleVersion, remoteConfig, endpoints)
	if err != nil {
		app.Logger().Fatal(app.Context(), err)
	}

	return boot
}

func bootstrap(isDev bool, app *app.Application, moduleVersion string, remoteConfig interface{}, endpoints []cluster.EndpointDescriptor) (*Bootstrap, error) {
	localConfig := LocalConfig{}
	err := app.Config().Read(&localConfig)
	if err != nil {
		return nil, errors.WithMessage(err, "read local config")
	}
	if localConfig.GrpcInnerAddress.Port != localConfig.GrpcOuterAddress.Port {
		return nil, errors.Errorf("grpcInnerAddress.port is not equal grpcOuterAddress.port. potential mistake")
	}

	configServiceHosts, err := parseConfigServiceHosts(localConfig.ConfigServiceAddress)
	if err != nil {
		return nil, errors.WithMessage(err, "parse config service hosts")
	}

	broadcastHost := localConfig.GrpcOuterAddress.IP
	if broadcastHost == "" {
		broadcastHost, err = resolveHost(configServiceHosts[0])
		if err != nil {
			return nil, errors.WithMessage(err, "resolve local host")
		}
	}

	moduleInfo := cluster.ModuleInfo{
		ModuleName:    localConfig.ModuleName,
		ModuleVersion: moduleVersion,
		GrpcOuterAddress: cluster.AddressConfiguration{
			IP:   broadcastHost,
			Port: strconv.Itoa(localConfig.GrpcOuterAddress.Port),
		},
		Endpoints: endpoints,
	}

	schema := schema.GenerateConfigSchema(remoteConfig)
	schemaData, err := json.Marshal(schema)
	if err != nil {
		return nil, errors.WithMessage(err, "marshal schema")
	}
	defaultConfig, err := readDefaultRemoteConfig(isDev, localConfig)
	if err != nil {
		return nil, errors.WithMessage(err, "read default remote config")
	}
	configData := cluster.ConfigData{
		Version: moduleVersion,
		Schema:  schemaData,
		Config:  defaultConfig,
	}

	clusterCli := cluster.NewClient(
		moduleInfo,
		configData,
		configServiceHosts,
		app.Logger(),
	)

	rc := rc.New(validator.Default, []byte(localConfig.RemoteConfigOverride))

	bindingAddress := net.JoinHostPort(localConfig.GrpcInnerAddress.IP, strconv.Itoa(localConfig.GrpcInnerAddress.Port))

	migrationsDir, err := migrationsDirPath(isDev, localConfig)
	if err != nil {
		return nil, errors.WithMessage(err, "resolve migrations dir path")
	}

	return &Bootstrap{
		App:            app,
		ClusterCli:     clusterCli,
		RemoteConfig:   rc,
		BindingAddress: bindingAddress,
		ModuleName:     localConfig.ModuleName,
		MigrationsDir:  migrationsDir,
	}, nil
}

func parseConfigServiceHosts(cfg ConfigServiceAddr) ([]string, error) {
	hosts := strings.Split(cfg.IP, ";")
	ports := strings.Split(cfg.Port, ";")
	if len(hosts) != len(ports) {
		return nil, errors.New("len(hosts) != len(ports)")
	}
	arr := make([]string, 0)
	for i, host := range hosts {
		arr = append(arr, net.JoinHostPort(host, ports[i]))
	}
	return arr, nil
}

func resolveHost(target string) (string, error) {
	conn, err := net.Dial("udp", target)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	return conn.LocalAddr().(*net.UDPAddr).IP.To4().String(), nil
}

func readDefaultRemoteConfig(isDev bool, cfg LocalConfig) (json2.RawMessage, error) {
	path, err := defaultRemoteConfigPath(isDev, cfg)
	if err != nil {
		return nil, errors.WithMessage(err, "resolve path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.WithMessage(err, "read file")
	}

	remoteConfig := json2.RawMessage{}
	err = json.Unmarshal(data, &remoteConfig)
	if err != nil {
		return nil, errors.WithMessage(err, "unmarshal json")
	}

	return remoteConfig, nil
}

func defaultRemoteConfigPath(isDev bool, cfg LocalConfig) (string, error) {
	if cfg.DefaultRemoteConfigPath != "" {
		return cfg.DefaultRemoteConfigPath, nil
	}

	if isDev {
		return "conf/default_remote_config.json", nil
	}

	return relativePathFromBin("default_remote_config.json")
}

func configFilePath(isDev bool) (string, error) {
	cfgPath := os.Getenv("APP_CONFIG_PATH")
	if cfgPath != "" {
		return cfgPath, nil
	}

	if isDev {
		return "./conf/config_dev.yml", nil
	}

	return relativePathFromBin("config.yml")
}

func migrationsDirPath(isDev bool, cfg LocalConfig) (string, error) {
	if cfg.MigrationsDirPath != "" {
		return cfg.MigrationsDirPath, nil
	}

	if isDev {
		return "./migrations", nil
	}

	return relativePathFromBin("migrations")
}

func relativePathFromBin(part string) (string, error) {
	ex, err := os.Executable()
	if err != nil {
		return "", errors.WithMessage(err, "get executable path")
	}
	return path.Join(path.Dir(ex), part), nil
}
