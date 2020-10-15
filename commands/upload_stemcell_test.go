package commands_test

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"github.com/pivotal-cf/om/api"
	"github.com/pivotal-cf/om/commands"
	"github.com/pivotal-cf/om/commands/fakes"
	"github.com/pivotal-cf/om/formcontent"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("UploadStemcell", func() {
	var (
		fakeService *fakes.UploadStemcellService
		multipart   *fakes.Multipart
		logger      *fakes.Logger
	)

	BeforeEach(func() {
		multipart = &fakes.Multipart{}
		fakeService = &fakes.UploadStemcellService{}
		logger = &fakes.Logger{}
	})

	Context("uploads the stemcell", func() {
		It("to all compatible products", func() {
			fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
			submission := formcontent.ContentSubmission{
				Content:       ioutil.NopCloser(strings.NewReader("")),
				ContentType:   "some content-type",
				ContentLength: 10,
			}
			multipart.FinalizeReturns(submission)

			fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{Stemcells: []string{}}, nil)

			command := commands.NewUploadStemcell(multipart, fakeService, logger)

			err := command.Execute([]string{
				"--stemcell", "/path/to/stemcell.tgz",
			})
			Expect(err).ToNot(HaveOccurred())

			key, file := multipart.AddFileArgsForCall(0)
			Expect(key).To(Equal("stemcell[file]"))
			Expect(file).To(Equal("/path/to/stemcell.tgz"))

			key, value := multipart.AddFieldArgsForCall(0)
			Expect(key).To(Equal("stemcell[floating]"))
			Expect(value).To(Equal("true"))

			Expect(fakeService.UploadStemcellArgsForCall(0)).To(Equal(api.StemcellUploadInput{
				ContentLength: 10,
				Stemcell:      ioutil.NopCloser(strings.NewReader("")),
				ContentType:   "some content-type",
			}))

			Expect(multipart.FinalizeCallCount()).To(Equal(1))

			format, v := logger.PrintfArgsForCall(0)
			Expect(fmt.Sprintf(format, v...)).To(Equal("processing stemcell"))

			format, v = logger.PrintfArgsForCall(1)
			Expect(fmt.Sprintf(format, v...)).To(Equal("beginning stemcell upload to Ops Manager"))

			format, v = logger.PrintfArgsForCall(2)
			Expect(fmt.Sprintf(format, v...)).To(Equal("finished upload"))
		})

		Context("floating", func() {
			var command commands.UploadStemcell
			BeforeEach(func() {
				fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
				submission := formcontent.ContentSubmission{
					ContentLength: 10,
					Content:       ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
				}
				multipart.FinalizeReturns(submission)

				fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{Stemcells: []string{}}, nil)

				command = commands.NewUploadStemcell(multipart, fakeService, logger)
			})

			It("disables floating", func() {
				err := command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
					"--floating", "false",
				})
				Expect(err).ToNot(HaveOccurred())

				key, file := multipart.AddFileArgsForCall(0)
				Expect(key).To(Equal("stemcell[file]"))
				Expect(file).To(Equal("/path/to/stemcell.tgz"))

				key, value := multipart.AddFieldArgsForCall(0)
				Expect(key).To(Equal("stemcell[floating]"))
				Expect(value).To(Equal("false"))

				Expect(fakeService.UploadStemcellArgsForCall(0)).To(Equal(api.StemcellUploadInput{
					ContentLength: 10,
					Stemcell:      ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
				}))

				Expect(multipart.FinalizeCallCount()).To(Equal(1))

				format, v := logger.PrintfArgsForCall(0)
				Expect(fmt.Sprintf(format, v...)).To(Equal("processing stemcell"))

				format, v = logger.PrintfArgsForCall(1)
				Expect(fmt.Sprintf(format, v...)).To(Equal("beginning stemcell upload to Ops Manager"))

				format, v = logger.PrintfArgsForCall(2)
				Expect(fmt.Sprintf(format, v...)).To(Equal("finished upload"))
			})

			It("only accepts true and false", func() {
				err := command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
					"--floating", "flalsee",
				})
				Expect(err).To(MatchError(ContainSubstring("--floating must be \"true\" or \"false\". Default: true")))

				err = command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
					"--floating", "trurure",
				})
				Expect(err).To(MatchError(ContainSubstring("--floating must be \"true\" or \"false\". Default: true")))

				err = command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
					"--floating", "true",
				})
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("the product fails to upload the first time with a retryable error", func() {
			It("tries again", func() {
				fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
				submission := formcontent.ContentSubmission{
					Content:       ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
					ContentLength: 10,
				}
				multipart.FinalizeReturns(submission)

				fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{Stemcells: []string{}}, nil)

				command := commands.NewUploadStemcell(multipart, fakeService, logger)

				fakeService.UploadStemcellReturnsOnCall(0, api.StemcellUploadOutput{}, fmt.Errorf("some upload error: %w", io.EOF))
				fakeService.UploadStemcellReturnsOnCall(1, api.StemcellUploadOutput{}, nil)

				err := command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
				})
				Expect(err).ToNot(HaveOccurred())

				Expect(multipart.AddFileCallCount()).To(Equal(2))
				Expect(multipart.FinalizeCallCount()).To(Equal(2))
				Expect(multipart.ResetCallCount()).To(Equal(1))

				Expect(fakeService.UploadStemcellCallCount()).To(Equal(2))
			})
		})

		When("the product fails to upload three times", func() {
			It("returns an error", func() {
				fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
				submission := formcontent.ContentSubmission{
					Content:       ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
					ContentLength: 10,
				}
				multipart.FinalizeReturns(submission)

				fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{Stemcells: []string{}}, nil)

				command := commands.NewUploadStemcell(multipart, fakeService, logger)

				fakeService.UploadStemcellReturns(api.StemcellUploadOutput{}, fmt.Errorf("some upload error: %w", io.EOF))

				err := command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
				})
				Expect(err).To(MatchError(ContainSubstring("EOF")))

				Expect(multipart.AddFileCallCount()).To(Equal(3))
				Expect(multipart.FinalizeCallCount()).To(Equal(3))
				Expect(multipart.ResetCallCount()).To(Equal(2))

				Expect(fakeService.UploadStemcellCallCount()).To(Equal(3))
			})
		})
	})

	When("the stemcell already exists", func() {
		Context("and force is not specified", func() {
			It("exits successfully without uploading", func() {
				submission := formcontent.ContentSubmission{
					ContentLength: 10,
					Content:       ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
				}
				multipart.FinalizeReturns(submission)
				fakeService.CheckStemcellAvailabilityReturns(true, nil)

				command := commands.NewUploadStemcell(multipart, fakeService, logger)

				err := command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
				})
				Expect(err).ToNot(HaveOccurred())

				format, v := logger.PrintfArgsForCall(1)
				Expect(fmt.Sprintf(format, v...)).To(Equal("stemcell has already been uploaded"))
			})
		})

		Context("and force is specified", func() {
			It("uploads the stemcell", func() {
				submission := formcontent.ContentSubmission{
					Content:       ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
					ContentLength: 10,
				}
				multipart.FinalizeReturns(submission)

				fakeService.CheckStemcellAvailabilityReturns(true, nil)

				command := commands.NewUploadStemcell(multipart, fakeService, logger)

				err := command.Execute([]string{
					"--stemcell", "/path/to/stemcell.tgz",
					"--force",
				})
				Expect(err).ToNot(HaveOccurred())

				key, file := multipart.AddFileArgsForCall(0)
				Expect(key).To(Equal("stemcell[file]"))
				Expect(file).To(Equal("/path/to/stemcell.tgz"))
				Expect(fakeService.UploadStemcellArgsForCall(0)).To(Equal(api.StemcellUploadInput{
					ContentLength: 10,
					Stemcell:      ioutil.NopCloser(strings.NewReader("")),
					ContentType:   "some content-type",
				}))

				Expect(multipart.FinalizeCallCount()).To(Equal(1))

				format, v := logger.PrintfArgsForCall(0)
				Expect(fmt.Sprintf(format, v...)).To(Equal("beginning stemcell upload to Ops Manager"))

				format, v = logger.PrintfArgsForCall(1)
				Expect(fmt.Sprintf(format, v...)).To(Equal("finished upload"))
			})
		})
	})

	When("the --shasum flag is defined", func() {
		It("proceeds normally when the sha sums match", func() {
			fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
			file, err := ioutil.TempFile("", "test-file.tgz")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(file.Name())

			_, err = file.WriteString("testing-shasum")
			Expect(err).ToNot(HaveOccurred())
			err = file.Close()
			Expect(err).ToNot(HaveOccurred())

			submission := formcontent.ContentSubmission{
				ContentLength: 10,
				Content:       ioutil.NopCloser(strings.NewReader("")),
				ContentType:   "some content-type",
			}
			multipart.FinalizeReturns(submission)

			fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{Stemcells: []string{}}, nil)

			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			err = command.Execute([]string{
				"--stemcell", file.Name(),
				"--shasum", "2815ab9694a4a2cfd59424a734833010e143a0b2db20be3741507f177f289f44",
			})
			Expect(err).ToNot(HaveOccurred())
			format, v := logger.PrintfArgsForCall(0)
			Expect(fmt.Sprintf(format, v...)).To(ContainSubstring("expected shasum matches stemcell shasum."))
		})
		It("returns an error when the sha sums don't match", func() {
			file, err := ioutil.TempFile("", "test-file.tgz")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(file.Name())

			_, err = file.WriteString("testing-shasum")
			Expect(err).ToNot(HaveOccurred())
			err = file.Close()
			Expect(err).ToNot(HaveOccurred())

			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			err = command.Execute([]string{
				"--stemcell", file.Name(),
				"--shasum", "not-the-correct-shasum",
			})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("expected shasum not-the-correct-shasum does not match file shasum 2815ab9694a4a2cfd59424a734833010e143a0b2db20be3741507f177f289f44"))
		})
		It("fails when the file can not calculate a shasum", func() {
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			err := command.Execute([]string{
				"--stemcell", "/path/to/testing.tgz",
				"--shasum", "2815ab9694a4a2cfd59424a734833010e143a0b2db20be3741507f177f289f44",
			})
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("open /path/to/testing.tgz: no such file or directory"))
		})
	})

	When("the diagnostic report is unavailable", func() {
		It("uploads the stemcell", func() {
			fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
			submission := formcontent.ContentSubmission{
				ContentLength: 10,
				Content:       ioutil.NopCloser(strings.NewReader("")),
				ContentType:   "some content-type",
			}
			multipart.FinalizeReturns(submission)

			fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{}, api.DiagnosticReportUnavailable{})

			command := commands.NewUploadStemcell(multipart, fakeService, logger)

			err := command.Execute([]string{
				"--stemcell", "/path/to/stemcell.tgz",
			})
			Expect(err).ToNot(HaveOccurred())

			key, file := multipart.AddFileArgsForCall(0)
			Expect(key).To(Equal("stemcell[file]"))
			Expect(file).To(Equal("/path/to/stemcell.tgz"))
			Expect(fakeService.UploadStemcellArgsForCall(0)).To(Equal(api.StemcellUploadInput{
				ContentLength: 10,
				Stemcell:      ioutil.NopCloser(strings.NewReader("")),
				ContentType:   "some content-type",
			}))

			Expect(multipart.FinalizeCallCount()).To(Equal(1))

			format, v := logger.PrintfArgsForCall(0)
			Expect(fmt.Sprintf(format, v...)).To(Equal("processing stemcell"))

			format, v = logger.PrintfArgsForCall(1)
			Expect(fmt.Sprintf(format, v...)).To(Equal("beginning stemcell upload to Ops Manager"))

			format, v = logger.PrintfArgsForCall(2)
			Expect(fmt.Sprintf(format, v...)).To(Equal("finished upload"))
		})
	})

	When("config file is provided", func() {
		var (
			configFile *os.File
			file       *os.File
		)

		BeforeEach(func() {
			var err error
			configContent := `
shasum: 2815ab9694a4a2cfd59424a734833010e143a0b2db20be3741507f177f289f44
`
			configFile, err = ioutil.TempFile("", "")
			Expect(err).ToNot(HaveOccurred())

			_, err = configFile.WriteString(configContent)
			Expect(err).ToNot(HaveOccurred())

			file, err = ioutil.TempFile("", "test-file.tgz")
			Expect(err).ToNot(HaveOccurred())

			_, err = file.WriteString("testing-shasum")
			Expect(err).ToNot(HaveOccurred())
			err = file.Close()
			Expect(err).ToNot(HaveOccurred())

			submission := formcontent.ContentSubmission{
				ContentLength: 10,
				Content:       ioutil.NopCloser(strings.NewReader("")),
				ContentType:   "some content-type",
			}
			multipart.FinalizeReturns(submission)

			fakeService.GetDiagnosticReportReturns(api.DiagnosticReport{Stemcells: []string{}}, nil)
		})

		AfterEach(func() {
			err := os.Remove(configFile.Name())
			Expect(err).ToNot(HaveOccurred())

			err = os.Remove(file.Name())
			Expect(err).ToNot(HaveOccurred())
		})

		It("reads configuration from config file", func() {
			fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			err := command.Execute([]string{
				"--stemcell", file.Name(),
				"--config", configFile.Name(),
			})
			Expect(err).ToNot(HaveOccurred())
			format, v := logger.PrintfArgsForCall(0)
			Expect(fmt.Sprintf(format, v...)).To(ContainSubstring("expected shasum matches stemcell shasum."))
		})
	})

	When("an unknown flag is provided", func() {
		It("returns an error", func() {
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			err := command.Execute([]string{"--badflag"})
			Expect(err).To(MatchError("could not parse upload-stemcell flags: flag provided but not defined: -badflag"))
		})
	})

	When("the --stemcell flag is missing", func() {
		It("returns an error", func() {
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			err := command.Execute([]string{})
			Expect(err).To(MatchError("could not parse upload-stemcell flags: missing required flag \"--stemcell\""))
		})
	})

	When("the file cannot be opened", func() {
		It("returns an error", func() {
			fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			multipart.AddFileReturns(errors.New("bad file"))

			err := command.Execute([]string{"--stemcell", "/some/path"})
			Expect(err).To(MatchError("failed to upload stemcell: bad file"))
		})
	})

	When("the stemcell cannot be uploaded", func() {
		It("returns an error", func() {
			fakeService.InfoReturns(api.Info{Version: "2.2-build.1"}, nil)
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			fakeService.UploadStemcellReturns(api.StemcellUploadOutput{}, errors.New("some stemcell error"))

			err := command.Execute([]string{"--stemcell", "/some/path"})
			Expect(err).To(MatchError("failed to upload stemcell: some stemcell error"))
		})
	})

	When("the stemcell availability cannot be fetched", func() {
		It("returns an error", func() {
			command := commands.NewUploadStemcell(multipart, fakeService, logger)
			fakeService.CheckStemcellAvailabilityReturns(false, errors.New("some diagnostic error"))

			err := command.Execute([]string{"--stemcell", "/some/path"})
			Expect(err.Error()).To(ContainSubstring("some diagnostic error"))
		})
	})
})
