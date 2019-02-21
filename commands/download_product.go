package commands

import (
	"encoding/json"
	"fmt"
	"github.com/graymeta/stow"
	"github.com/pivotal-cf/pivnet-cli/filter"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/pivotal-cf/go-pivnet"
	pivnetlog "github.com/pivotal-cf/go-pivnet/logger"
	"github.com/pivotal-cf/jhanda"
	"github.com/pivotal-cf/om/validator"
	"github.com/pivotal-cf/pivnet-cli/gp"
)

const DownloadProductOutputFilename = "download-file.json"

type outputList struct {
	ProductPath     string `json:"product_path,omitempty"`
	ProductSlug     string `json:"product_slug,omitempty"`
	StemcellPath    string `json:"stemcell_path,omitempty"`
	StemcellVersion string `json:"stemcell_version,omitempty"`
}

type ProductDownloader interface {
	GetAllProductVersions(slug string) ([]string, error)
	GetLatestProductFile(slug, version, glob string) (*FileArtifact, error)
	DownloadProductToFile(fa *FileArtifact, file *os.File) error
	DownloadProductStemcell(fa *FileArtifact) (*stemcell, error)
}

func DefaultPivnetFactory(config pivnet.ClientConfig, logger pivnetlog.Logger) PivnetDownloader {
	return gp.NewClient(config, logger)
}

type DefaultStow struct{}

func (d DefaultStow) Dial(kind string, config Config) (stow.Location, error) {
	location, err := stow.Dial(kind, config)
	return location, err
}
func (d DefaultStow) Walk(container stow.Container, prefix string, pageSize int, fn stow.WalkFunc) error {
	return stow.Walk(container, prefix, pageSize, fn)
}

type DownloadProduct struct {
	environFunc    func() []string
	logger         pivnetlog.Logger
	progressWriter io.Writer
	pivnetFactory  PivnetFactory
	stower         Stower
	downloadClient ProductDownloader
	Options        struct {
		Blobstore           string   `long:"blobstore"             short:"b"  description:"specify an blobstore to use if not downloading from pivnet"`
		ConfigFile          string   `long:"config"                short:"c"  description:"path to yml file for configuration (keys must match the following command line flags)"`
		OutputDir           string   `long:"output-directory"      short:"o"  description:"directory path to which the file will be outputted. File Name will be preserved from Pivotal Network" required:"true"`
		PivnetFileGlob      string   `long:"pivnet-file-glob"      short:"f"  description:"glob to match files within Pivotal Network product to be downloaded." required:"true"`
		PivnetProductSlug   string   `long:"pivnet-product-slug"   short:"p"  description:"path to product" required:"true"`
		PivnetToken         string   `long:"pivnet-api-token"      short:"t"  description:"API token to use when interacting with Pivnet. Can be retrieved from your profile page in Pivnet." required:"true"`
		ProductVersion      string   `long:"product-version"       short:"v"  description:"version of the product-slug to download files from. Incompatible with --product-version-regex flag."`
		ProductVersionRegex string   `long:"product-version-regex" short:"r"  description:"regex pattern matching versions of the product-slug to download files from. Highest-versioned match will be used. Incompatible with --product-version flag."`
		S3Bucket            string   `long:"s3-bucket"                        description:"bucket name where the product resides in the s3 compatible blobstore"`
		S3AccessKeyID       string   `long:"s3-access-key-id"                 description:"access key for the s3 compatible blobstore"`
		S3SecretAccessKey   string   `long:"s3-secret-access-key"             description:"secret key for the s3 compatible blobstore"`
		S3RegionName        string   `long:"s3-region-name"                   description:"bucket region in the s3 compatible blobstore. If not using AWS, this value is 'region'"`
		S3Endpoint          string   `long:"s3-endpoint"                      description:"the endpoint to access the s3 compatible blobstore. If not using AWS, this is required"`
		S3DisableSSL        bool     `long:"s3-disable-ssl"                   description:"whether to disable ssl validation when contacting  the s3 compatible blobstore"`
		S3EnableV2Signing   bool     `long:"s3-enable-v2-signing"             description:"whether to use v2 signing with your s3 compatible blobstore. (if you don't know what this is, leave blank, or set to 'false')"`
		Stemcell            bool     `long:"download-stemcell"                cription:"no-op for backwards compatibility"`
		StemcellIaas        string   `long:"stemcell-iaas"                    description:"download the latest available stemcell for the product for the specified iaas. for example 'vsphere' or 'vcloud' or 'openstack' or 'google' or 'azure' or 'aws'"`
		VarsEnv             []string `long:"vars-env"                         description:"load variables from environment variables matching the provided prefix (e.g.: 'MY' to load MY_var=value)"`
		VarsFile            []string `long:"vars-file"             short:"l"  description:"load variables from a YAML file"`
	}
}

func NewDownloadProduct(
	environFunc func() []string,
	logger pivnetlog.Logger,
	progressWriter io.Writer,
	factory PivnetFactory,
	stower Stower,
) *DownloadProduct {
	return &DownloadProduct{
		environFunc:    environFunc,
		logger:         logger,
		progressWriter: progressWriter,
		pivnetFactory:  factory,
		stower:         stower,
	}
}

func (c DownloadProduct) Usage() jhanda.Usage {
	return jhanda.Usage{
		Description:      "This command attempts to download a single product file from Pivotal Network. The API token used must be associated with a user account that has already accepted the EULA for the specified product",
		ShortDescription: "downloads a specified product file from Pivotal Network",
		Flags:            c.Options,
	}
}

func (c *DownloadProduct) Execute(args []string) error {
	err := loadConfigFile(args, &c.Options, c.environFunc)
	if err != nil {
		return fmt.Errorf("could not parse download-product flags: %s", err)
	}

	err = c.validate()
	if err != nil {
		return err
	}

	err = c.createClient()
	if err != nil {
		return err
	}

	productVersion, err := c.determineProductVersion()
	if err != nil {
		return err
	}

	prefixPath := fmt.Sprintf("[%s,%s]", c.Options.PivnetProductSlug, productVersion)
	productFileName, productFileArtifact, err := c.downloadProductFile(c.Options.PivnetProductSlug, productVersion, c.Options.PivnetFileGlob, prefixPath)
	if err != nil {
		return fmt.Errorf("could not download product: %s", err)
	}

	if c.Options.StemcellIaas == "" {
		return c.writeOutputFile(productFileName, "", "")
	}

	c.logger.Info("Downloading stemcell")

	nameParts := strings.Split(productFileName, ".")
	if nameParts[len(nameParts)-1] != "pivotal" {
		c.logger.Info("the downloaded file is not a .pivotal file. Not determining and fetching required stemcell.")
		return nil
	}

	stemcell, err := c.downloadClient.DownloadProductStemcell(productFileArtifact)
	if err != nil {
		return fmt.Errorf("could not information about stemcell: %s", err)
	}

	prefixPath = ""
	stemcellFileName, _, err := c.downloadProductFile(stemcell.Slug, stemcell.Version, fmt.Sprintf("*%s*", c.Options.StemcellIaas), prefixPath)
	if err != nil {
		return fmt.Errorf("could not download stemcell: %s", err)
	}

	return c.writeOutputFile(productFileName, stemcellFileName, stemcell.Version)
}

func (c DownloadProduct) createS3Config() S3Configuration {
	config := S3Configuration{
		Bucket:          c.Options.S3Bucket,
		AccessKeyID:     c.Options.S3AccessKeyID,
		SecretAccessKey: c.Options.S3SecretAccessKey,
		RegionName:      c.Options.S3RegionName,
		Endpoint:        c.Options.S3Endpoint,
		DisableSSL:      c.Options.S3DisableSSL,
		EnableV2Signing: c.Options.S3EnableV2Signing,
	}
	return config
}

func (c *DownloadProduct) determineProductVersion() (string, error) {
	if c.Options.ProductVersionRegex != "" {
		re, err := regexp.Compile(c.Options.ProductVersionRegex)
		if err != nil {
			return "", fmt.Errorf("could not compile regex: %s: %s", c.Options.ProductVersionRegex, err)
		}

		productVersions, err := c.downloadClient.GetAllProductVersions(c.Options.PivnetProductSlug)
		if err != nil {
			return "", err
		}

		var versions version.Collection
		for _, productVersion := range productVersions {
			if !re.MatchString(productVersion) {
				continue
			}

			v, err := version.NewVersion(productVersion)
			if err != nil {
				c.logger.Info(fmt.Sprintf("warning: could not parse semver version from: %s", productVersion))
				continue
			}
			versions = append(versions, v)
		}

		sort.Sort(versions)

		if len(versions) == 0 {
			return "", fmt.Errorf("no valid versions found for product '%s'", c.Options.PivnetProductSlug)
		}

		return versions[len(versions)-1].Original(), nil
	}
	return c.Options.ProductVersion, nil
}

func (c *DownloadProduct) createClient() error {
	var err error
	switch c.Options.Blobstore {
	case "s3":
		config := c.createS3Config()
		c.downloadClient, err = NewS3Client(c.stower, config, os.Stdout)
		if err != nil {
			return fmt.Errorf("could not create an s3 client: %s", err)
		}
	default:
		filter := filter.NewFilter(c.logger)
		c.downloadClient = NewPivnetClient(c.logger, c.progressWriter, c.pivnetFactory, c.Options.PivnetToken, filter)
	}
	return nil
}

func (c *DownloadProduct) validate() error {
	if c.Options.ProductVersionRegex != "" && c.Options.ProductVersion != "" {
		return fmt.Errorf("cannot use both --product-version and --product-version-regex; please choose one or the other")
	}

	if c.Options.ProductVersionRegex == "" && c.Options.ProductVersion == "" {
		return fmt.Errorf("no version information provided; please provide either --product-version or --product-version-regex")
	}
	return nil
}

func (c DownloadProduct) writeOutputFile(productFileName string, stemcellFileName string, stemcellVersion string) error {
	c.logger.Info(fmt.Sprintf("Writing a list of downloaded artifact to %s", DownloadProductOutputFilename))
	outputList := outputList{
		ProductPath:     productFileName,
		StemcellPath:    stemcellFileName,
		ProductSlug:     c.Options.PivnetProductSlug,
		StemcellVersion: stemcellVersion,
	}

	outputFile, err := os.Create(path.Join(c.Options.OutputDir, DownloadProductOutputFilename))
	if err != nil {
		return fmt.Errorf("could not create %s: %s", DownloadProductOutputFilename, err)
	}
	defer outputFile.Close()

	return json.NewEncoder(outputFile).Encode(outputList)
}

func (c *DownloadProduct) downloadProductFile(slug, version, glob, prefixPath string) (string, *FileArtifact, error) {
	fileArtifact, err := c.downloadClient.GetLatestProductFile(slug, version, glob)
	if err != nil {
		return "", nil, err
	}

	expectedFileFormat := regexp.MustCompile(fmt.Sprintf(`^\[%s,%s\]`, slug, regexp.QuoteMeta(version)))
	var productFilePath string
	if expectedFileFormat.MatchString(path.Base(fileArtifact.Name)) {
		productFilePath = path.Join(c.Options.OutputDir, path.Base(fileArtifact.Name))
	} else {
		productFilePath = path.Join(c.Options.OutputDir, prefixPath+path.Base(fileArtifact.Name))
	}

	exist, err := checkFileExists(productFilePath, fileArtifact.sha256)
	if err != nil {
		return productFilePath, nil, err
	}

	if exist {
		c.logger.Info(fmt.Sprintf("%s already exists, skip downloading", productFilePath))
		return productFilePath, fileArtifact, nil
	}

	productFile, err := os.Create(productFilePath)
	if err != nil {
		return "", nil, fmt.Errorf("could not create file %s: %s", productFilePath, err)
	}
	defer productFile.Close()

	return productFilePath, fileArtifact, c.downloadClient.DownloadProductToFile(fileArtifact, productFile)
}

func checkFileExists(path, expectedSum string) (bool, error) {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("failed to get file information: %s", err)
		}
	}

	validate := validator.NewSHA256Calculator()
	sum, err := validate.Checksum(path)
	if err != nil {
		return false, fmt.Errorf("failed to calculate the checksum: %s", err)
	}

	return sum == expectedSum, nil
}
