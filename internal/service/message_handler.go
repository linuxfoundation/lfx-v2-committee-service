// Copyright The Linux Foundation and each contributor to LFX.
// SPDX-License-Identifier: MIT

package service

import (
	"github.com/linuxfoundation/lfx-v2-committee-service/internal/domain/port"
)

// handlerConfig aggregates all dependencies for message handler option functions.
// Required: committeeReader, committeeWriterOrchestrator, committeeWriter, committeePublisher.
// Optional: emailSender, inviteSender, userReader, projectReader, linkReader,
// lfxSelfServeBaseURL, weeklyBriefGenerator — sub-handlers that accept optional deps
// nil-guard at the point of use and degrade gracefully when they are absent.
type handlerConfig struct {
	committeeReader             CommitteeReader
	committeeWriterOrchestrator CommitteeWriter
	committeeWriter             port.CommitteeWriter
	committeePublisher          port.CommitteePublisher
	emailSender                 port.EmailSender
	inviteSender                port.InviteSender
	userReader                  port.UserReader
	projectReader               port.ProjectReader
	linkReader                  port.CommitteeLinkReader
	lfxSelfServeBaseURL         string
	weeklyBriefGenerator        GroupWeeklyBriefGenerator
}

// messageHandlerOrchestratorOption defines a function type for setting options.
type messageHandlerOrchestratorOption func(*handlerConfig)

// WithCommitteeReaderForMessageHandler sets the committee reader for message handler
func WithCommitteeReaderForMessageHandler(reader CommitteeReader) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.committeeReader = reader
	}
}

// WithCommitteeWriterForMessageHandler sets the committee writer for message handler
func WithCommitteeWriterForMessageHandler(writer port.CommitteeWriter) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.committeeWriter = writer
	}
}

// WithCommitteePublisherForMessageHandler sets the committee publisher for message handler
func WithCommitteePublisherForMessageHandler(publisher port.CommitteePublisher) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.committeePublisher = publisher
	}
}

// WithCommitteeWriterOrchestratorForMessageHandler sets the service-level committee writer for member sync
func WithCommitteeWriterOrchestratorForMessageHandler(writer CommitteeWriter) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.committeeWriterOrchestrator = writer
	}
}

// WithEmailSenderForMessageHandler sets the email sender for notification emails.
func WithEmailSenderForMessageHandler(sender port.EmailSender) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.emailSender = sender
	}
}

// WithInviteSenderForMessageHandler sets the invite sender for non-LFID users.
func WithInviteSenderForMessageHandler(sender port.InviteSender) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.inviteSender = sender
	}
}

// WithLFXSelfServeBaseURLForMessageHandler sets the base URL used to build links in notification emails.
func WithLFXSelfServeBaseURLForMessageHandler(baseURL string) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.lfxSelfServeBaseURL = baseURL
	}
}

// WithUserReaderForMessageHandler sets the user reader used to resolve display names for notification emails.
func WithUserReaderForMessageHandler(reader port.UserReader) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.userReader = reader
	}
}

// WithProjectReaderForMessageHandler sets the project reader used for the project-writers fallback
// in application submitted notifications.
func WithProjectReaderForMessageHandler(reader port.ProjectReader) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.projectReader = reader
	}
}

// WithLinkReaderForMessageHandler sets the link reader used to resolve folder names in document/link notifications.
func WithLinkReaderForMessageHandler(reader port.CommitteeLinkReader) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.linkReader = reader
	}
}

// WithGroupWeeklyBriefGeneratorForMessageHandler sets the generator used to
// fulfill async weekly-brief generation requests.
func WithGroupWeeklyBriefGeneratorForMessageHandler(generator GroupWeeklyBriefGenerator) messageHandlerOrchestratorOption {
	return func(c *handlerConfig) {
		c.weeklyBriefGenerator = generator
	}
}

// messageHandlerAggregator embeds all sub-handlers to implement port.MessageHandler.
type messageHandlerAggregator struct {
	port.CommitteeAttributeHandler
	port.CommitteeMemberHandler
	port.CommitteeMailingListHandler
	port.CommitteeNotificationHandler
	port.WeeklyBriefGenerateHandler
	port.UserEventHandler
	port.UserEmailSyncHandler
}

// NewMessageHandlerOrchestrator creates a new message handler by constructing focused sub-handlers
// and combining them into a thin aggregator that implements port.MessageHandler.
func NewMessageHandlerOrchestrator(opts ...messageHandlerOrchestratorOption) port.MessageHandler {
	config := &handlerConfig{}
	for _, opt := range opts {
		opt(config)
	}

	userEventH := NewUserEventHandler(config.committeeReader, config.committeeWriterOrchestrator, config.userReader)
	return &messageHandlerAggregator{
		CommitteeAttributeHandler:   NewCommitteeAttributeHandler(config.committeeReader),
		CommitteeMemberHandler:      NewCommitteeMemberHandler(config.committeeReader, config.committeeWriterOrchestrator, config.committeeWriter, config.committeePublisher),
		CommitteeMailingListHandler: NewCommitteeMailingListHandler(config.committeeReader, config.committeeWriter, config.committeePublisher),
		CommitteeNotificationHandler: NewCommitteeNotificationHandler(
			config.committeeReader,
			config.committeeWriterOrchestrator,
			config.committeePublisher,
			config.emailSender,
			config.inviteSender,
			config.userReader,
			config.linkReader,
			config.lfxSelfServeBaseURL,
			config.projectReader,
		),
		WeeklyBriefGenerateHandler: newWeeklyBriefHandlerOrNoop(config.weeklyBriefGenerator, config.committeeReader),
		UserEventHandler:           userEventH,
		UserEmailSyncHandler:       userEventH,
	}
}
