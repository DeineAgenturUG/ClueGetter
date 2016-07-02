// ClueGetter - Does things with mail
//
// Copyright 2016 Dolf Schimmel, Freeaqingme.
//
// This Source Code Form is subject to the terms of the two-clause BSD license.
// For its contents, please refer to the LICENSE file.
//
package core

import (
	"fmt"
)

func init() {
	reportMessageId := make(chan string, 64)
	learnMessage := make(chan string, 64)
	enable := func() bool { return Config.Bayes.Enabled }
	init := func() {
		bayesStart(reportMessageId, learnMessage)
	}

	ModuleRegister(&ModuleOld{
		name:   "bayes",
		enable: &enable,
		init:   &init,
		rpc: map[string]chan string{
			"bayes!reportMessageId": reportMessageId,
			"bayes!learn":           learnMessage,
		},
		// TODO: HTTP Interface to report HAM/SPAM
	})
}

func bayesStart(reportMessageId, learnMessage chan string) {
	go bayesHandleReportMessageIdQueue(reportMessageId)
	go bayesHandleLearnQueue(learnMessage)
}

func bayesHandleReportMessageIdQueue(reportMessageIdQueue chan string) {
	for report := range reportMessageIdQueue {
		go bayesHandleReportMessageIdQueueItem(report)
	}
}

func bayesHandleLearnQueue(learnMessageQueue chan string) {
	for lesson := range learnMessageQueue {
		go bayesLearn(lesson)
	}
}

func bayesHandleReportMessageIdQueueItem(item string) {
	CluegetterRecover("bayesHandleReportMessageIdQueueItem")

	rpc := &Rpc{}
	err := rpc.Unmarshal([]byte(item))
	if err != nil {
		Log.Errorf("Could not unmarshal RPC Message Bayes_Learn_Message_Id:", err.Error())
		return
	}

	if rpc.Name != "Bayes_Learn_Message_Id" || rpc.Bayes_Learn_Message_Id == nil {
		Log.Errorf("Invalid RPC Message Bayes_Learn_Message_Id")
		return
	}
	rpcMsg := rpc.Bayes_Learn_Message_Id

	msgBytes := messagePersistCache.getByMessageId(rpcMsg.MessageId)
	if len(msgBytes) == 0 {
		Log.Errorf("Could not retrieve message from cache with message-id %s",
			rpcMsg.MessageId)
		return
	}

	msg, err := messagePersistUnmarshalProto(msgBytes)
	if err != nil {
		Log.Errorf("Could not unmarshal message from cache: %s", err.Error())
		return
	}
	rpcName := "Bayes_Learn_Message"
	rpcOut := &Rpc{
		Name: rpcName,
		Bayes_Learn_Message: &Rpc__Bayes_Learn_Message{
			IsSpam:   rpcMsg.IsSpam,
			Message:  msg,
			Host:     rpcMsg.Host,
			Reporter: rpcMsg.Reporter,
			Reason:   rpcMsg.Reason,
		},
	}

	if rpcMsg.IsSpam {
		bayesAddToCorpus(true, msg, rpcMsg.MessageId, rpcMsg.Host, rpcMsg.Reporter, rpcMsg.Reason)
	} else {
		bayesAddToCorpus(false, msg, rpcMsg.MessageId, rpcMsg.Host, rpcMsg.Reporter, rpcMsg.Reason)
	}

	payload, err := rpcOut.Marshal()
	if err != nil {
		Log.Errorf("Could not marshal data-object to json: %s", err.Error())
		return
	}
	err = redisPublish(fmt.Sprintf("cluegetter!!bayes!learn"), payload)
	if err != nil {
		Log.Errorf("Error while reporting bayes message id: %s", err.Error())
	}
}

func bayesAddToCorpus(spam bool, msg *Proto_Message, messageId, host, reporter, reason string) {
	// TODO
}

func bayesReportMessageId(spam bool, messageId, host, reporter, reason string) {
	CluegetterRecover("bayesReportMessageId")
	if !Config.Bayes.Enabled {
		return
	}

	rpcName := "Bayes_Learn_Message_Id"
	payload := &Rpc{
		Name: rpcName,
		Bayes_Learn_Message_Id: &Rpc__Bayes_Learn_Message_Id{
			IsSpam:    spam,
			MessageId: messageId,
			Host:      host,
			Reporter:  reporter,
			Reason:    reason,
		},
	}

	key := fmt.Sprintf("cluegetter!%d!bayes!reportMessageId", instance)
	payloadBytes, _ := payload.Marshal()
	err := redisPublish(key, payloadBytes)

	if err != nil {
		Log.Errorf("Error while reporting bayes message id: %s", err.Error())
	}
}

func bayesLearn(item string) {
	rpc := &Rpc{}
	err := rpc.Unmarshal([]byte(item))
	if err != nil {
		Log.Errorf("Could not unmarshal RPC Message Bayes_Learn_Message:", err.Error())
		return
	}

	if rpc.Name != "Bayes_Learn_Message" || rpc.Bayes_Learn_Message == nil {
		Log.Errorf("Invalid RPC Message Bayes_Learn_Message")
		return
	}

	msg := rpc.Bayes_Learn_Message.Message.getAsMessage()
	for _, module := range cg.Modules() {
		go func(m Module, msg *Message, isSpam bool) {
			CluegetterRecover("bayesLearn." + m.Name())
			module.BayesLearn(msg, isSpam)
		}(module, msg, rpc.Bayes_Learn_Message.IsSpam)
	}
}
