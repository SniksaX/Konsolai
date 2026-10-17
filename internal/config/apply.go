package config

func applyOverlay(c *Config, o Overlay) {
	if o.LLM != nil {
		if o.LLM.Endpoint != nil {
			c.LLM.Endpoint = *o.LLM.Endpoint
		}
		if o.LLM.Model != nil {
			c.LLM.Model = *o.LLM.Model
		}
		if o.LLM.IdleTimeoutSeconds != nil {
			c.LLM.IdleTimeoutSeconds = *o.LLM.IdleTimeoutSeconds
		}
		if o.LLM.MaxTokens != nil {
			if o.LLM.MaxTokens.Answer != nil {
				c.LLM.MaxTokens.Answer = *o.LLM.MaxTokens.Answer
			}
			if o.LLM.MaxTokens.Lookup != nil {
				c.LLM.MaxTokens.Lookup = *o.LLM.MaxTokens.Lookup
			}
			if o.LLM.MaxTokens.Act != nil {
				c.LLM.MaxTokens.Act = *o.LLM.MaxTokens.Act
			}
			if o.LLM.MaxTokens.Build != nil {
				c.LLM.MaxTokens.Build = *o.LLM.MaxTokens.Build
			}
		}
	}
	if o.Fast != nil {
		if o.Fast.Enabled != nil {
			c.Fast.Enabled = *o.Fast.Enabled
		}
		if o.Fast.Endpoint != nil {
			c.Fast.Endpoint = *o.Fast.Endpoint
		}
		if o.Fast.Model != nil {
			c.Fast.Model = *o.Fast.Model
		}
		if o.Fast.ContextGuardRatio != nil {
			c.Fast.ContextGuardRatio = *o.Fast.ContextGuardRatio
		}
	}
	if o.Database != nil && o.Database.URL != nil {
		c.Database.URL = *o.Database.URL
	}
	if o.Memory != nil {
		if o.Memory.SessionIdleMinutes != nil {
			c.Memory.SessionIdleMinutes = *o.Memory.SessionIdleMinutes
		}
		if o.Memory.RetentionDays != nil {
			c.Memory.RetentionDays = *o.Memory.RetentionDays
		}
		if o.Memory.CompactThreshold != nil {
			c.Memory.CompactThreshold = *o.Memory.CompactThreshold
		}
	}
	if o.Context != nil {
		if o.Context.ElideTrigger != nil {
			c.Context.ElideTrigger = *o.Context.ElideTrigger
		}
		if o.Context.ElideTarget != nil {
			c.Context.ElideTarget = *o.Context.ElideTarget
		}
	}
	if o.Agent != nil {
		if o.Agent.BuildThinking != nil {
			c.Agent.BuildThinking = *o.Agent.BuildThinking
		}
		if o.Agent.IdenticalRejects != nil {
			c.Agent.IdenticalRejects = *o.Agent.IdenticalRejects
		}
		if o.Agent.MaxSteps != nil {
			if o.Agent.MaxSteps.Lookup != nil {
				c.Agent.MaxSteps.Lookup = *o.Agent.MaxSteps.Lookup
			}
			if o.Agent.MaxSteps.Act != nil {
				c.Agent.MaxSteps.Act = *o.Agent.MaxSteps.Act
			}
			if o.Agent.MaxSteps.Build != nil {
				c.Agent.MaxSteps.Build = *o.Agent.MaxSteps.Build
			}
		}
		if o.Agent.ExploreBudget != nil {
			e := o.Agent.ExploreBudget
			if e.Lookup != nil {
				c.Agent.ExploreBudget.Lookup = *e.Lookup
			}
			if e.LookupAnalysis != nil {
				c.Agent.ExploreBudget.LookupAnalysis = *e.LookupAnalysis
			}
			if e.Act != nil {
				c.Agent.ExploreBudget.Act = *e.Act
			}
			if e.Build != nil {
				c.Agent.ExploreBudget.Build = *e.Build
			}
			if e.BuildDocs != nil {
				c.Agent.ExploreBudget.BuildDocs = *e.BuildDocs
			}
			if e.BuildAnalysis != nil {
				c.Agent.ExploreBudget.BuildAnalysis = *e.BuildAnalysis
			}
		}
	}
	if o.RAG != nil {
		if o.RAG.Enabled != nil {
			c.RAG.Enabled = *o.RAG.Enabled
		}
		if o.RAG.EmbedEndpoint != nil {
			c.RAG.EmbedEndpoint = *o.RAG.EmbedEndpoint
		}
		if o.RAG.RerankEndpoint != nil {
			c.RAG.RerankEndpoint = *o.RAG.RerankEndpoint
		}
		if o.RAG.EmbedBatch != nil {
			c.RAG.EmbedBatch = *o.RAG.EmbedBatch
		}
		if o.RAG.Chunk != nil {
			ch := o.RAG.Chunk
			if ch.TurnTokens != nil {
				c.RAG.Chunk.TurnTokens = *ch.TurnTokens
			}
			if ch.WebTokens != nil {
				c.RAG.Chunk.WebTokens = *ch.WebTokens
			}
			if ch.WebOverlapTokens != nil {
				c.RAG.Chunk.WebOverlapTokens = *ch.WebOverlapTokens
			}
			if ch.CodeWindowLines != nil {
				c.RAG.Chunk.CodeWindowLines = *ch.CodeWindowLines
			}
			if ch.CodeOverlapLines != nil {
				c.RAG.Chunk.CodeOverlapLines = *ch.CodeOverlapLines
			}
			if ch.GoMaxDeclLines != nil {
				c.RAG.Chunk.GoMaxDeclLines = *ch.GoMaxDeclLines
			}
			if ch.GoSplitWindowLines != nil {
				c.RAG.Chunk.GoSplitWindowLines = *ch.GoSplitWindowLines
			}
			if ch.GoSplitOverlapLines != nil {
				c.RAG.Chunk.GoSplitOverlapLines = *ch.GoSplitOverlapLines
			}
		}
		if o.RAG.Search != nil {
			s := o.RAG.Search
			if s.VectorTop != nil {
				c.RAG.Search.VectorTop = *s.VectorTop
			}
			if s.TextTop != nil {
				c.RAG.Search.TextTop = *s.TextTop
			}
			if s.RRFK != nil {
				c.RAG.Search.RRFK = *s.RRFK
			}
			if s.RerankTop != nil {
				c.RAG.Search.RerankTop = *s.RerankTop
			}
			if s.K != nil {
				c.RAG.Search.K = *s.K
			}
			if s.RerankThreshold != nil {
				c.RAG.Search.RerankThreshold = *s.RerankThreshold
			}
		}
		if o.RAG.Index != nil {
			if o.RAG.Index.MaxFileKB != nil {
				c.RAG.Index.MaxFileKB = *o.RAG.Index.MaxFileKB
			}
			if o.RAG.Index.ConfirmOverMinutes != nil {
				c.RAG.Index.ConfirmOverMinutes = *o.RAG.Index.ConfirmOverMinutes
			}
		}
	}
	if o.UI != nil {
		if o.UI.DisplayLines != nil {
			c.UI.DisplayLines = *o.UI.DisplayLines
		}
		if o.UI.PasteChipLines != nil {
			c.UI.PasteChipLines = *o.UI.PasteChipLines
		}
		if o.UI.PasteChipChars != nil {
			c.UI.PasteChipChars = *o.UI.PasteChipChars
		}
		if o.UI.OutputKeepKB != nil {
			c.UI.OutputKeepKB = *o.UI.OutputKeepKB
		}
		if o.UI.Palette != nil {
			c.UI.Palette = *o.UI.Palette
		}
		if o.UI.Banner != nil {
			c.UI.Banner = *o.UI.Banner
		}
		if o.UI.Welcome != nil {
			c.UI.Welcome = *o.UI.Welcome
		}
		if o.UI.Theme != nil {
			c.UI.Theme = *o.UI.Theme
		}
		if o.UI.Appearance != nil {
			c.UI.Appearance = *o.UI.Appearance
		}
		if o.UI.CursorColor != nil {
			c.UI.CursorColor = *o.UI.CursorColor
		}
		if o.UI.CodeHighlight != nil {
			c.UI.CodeHighlight = *o.UI.CodeHighlight
		}
		if o.UI.ClearOnStart != nil {
			c.UI.ClearOnStart = *o.UI.ClearOnStart
		}
		if o.UI.StripEmoji != nil {
			c.UI.StripEmoji = *o.UI.StripEmoji
		}
		if o.UI.ProbeDeadlineMS != nil {
			c.UI.ProbeDeadlineMS = *o.UI.ProbeDeadlineMS
		}
		if o.UI.ProbeTimeoutMS != nil {
			c.UI.ProbeTimeoutMS = *o.UI.ProbeTimeoutMS
		}
		if o.UI.GitQueryMS != nil {
			c.UI.GitQueryMS = *o.UI.GitQueryMS
		}
		if o.UI.MemoryQueryMS != nil {
			c.UI.MemoryQueryMS = *o.UI.MemoryQueryMS
		}
		if o.UI.StartupCapMS != nil {
			c.UI.StartupCapMS = *o.UI.StartupCapMS
		}
		if o.UI.WelcomeCacheSeconds != nil {
			c.UI.WelcomeCacheSeconds = *o.UI.WelcomeCacheSeconds
		}
		if o.UI.ModelRateCalls != nil {
			c.UI.ModelRateCalls = *o.UI.ModelRateCalls
		}
		if o.UI.CtxWarnPct != nil {
			c.UI.CtxWarnPct = *o.UI.CtxWarnPct
		}
		if o.UI.CtxHotPct != nil {
			c.UI.CtxHotPct = *o.UI.CtxHotPct
		}
		if o.UI.CtxCells != nil {
			c.UI.CtxCells = *o.UI.CtxCells
		}
		if o.UI.CtxNarrowCols != nil {
			c.UI.CtxNarrowCols = *o.UI.CtxNarrowCols
		}
		if o.UI.Layout != nil {
			l := o.UI.Layout
			if l.TinyHeight != nil {
				c.UI.Layout.TinyHeight = *l.TinyHeight
			}
			if l.RecentHeight != nil {
				c.UI.Layout.RecentHeight = *l.RecentHeight
			}
			if l.TinyWidth != nil {
				c.UI.Layout.TinyWidth = *l.TinyWidth
			}
			if l.NarrowWidth != nil {
				c.UI.Layout.NarrowWidth = *l.NarrowWidth
			}
			if l.FrameWidth != nil {
				c.UI.Layout.FrameWidth = *l.FrameWidth
			}
		}
	}
	if o.Language != nil {
		if o.Language.Languages != nil {
			c.Language.Languages = append([]string(nil), (*o.Language.Languages)...)
		}
		if o.Language.Default != nil {
			c.Language.Default = *o.Language.Default
		}
	}
	if o.Safety != nil {
		if o.Safety.ExtraReadDeny != nil {
			c.Safety.ExtraReadDeny = append([]string(nil), (*o.Safety.ExtraReadDeny)...)
		}
		if o.Safety.ExtraCommandDeny != nil {
			c.Safety.ExtraCommandDeny = append([]string(nil), (*o.Safety.ExtraCommandDeny)...)
		}
	}
	if o.Router != nil {
		if o.Router.ActionVerbs != nil {
			c.Router.ActionVerbs = append([]string(nil), (*o.Router.ActionVerbs)...)
		}
		if o.Router.Targets != nil {
			c.Router.Targets = append([]string(nil), (*o.Router.Targets)...)
		}
	}
}
