package main

import (
	"google.golang.org/appengine"
	"google.golang.org/appengine/user"
	"net/http"
	"strings"
	"google.golang.org/appengine/log"
	"encoding/json"
	"golang.org/x/net/context"
	"time"
	"google.golang.org/appengine/mail"
	"github.com/dustin/go-humanize"
)

func init() {
	http.HandleFunc("/", home)
	http.HandleFunc("/login", login)
	http.HandleFunc("/tweet", handleTweet)
	http.HandleFunc("/logout", logout)
	http.HandleFunc("/profile", profile)
	http.Handle("/favicon.ico", http.NotFoundHandler())
	http.Handle("/public/", http.StripPrefix("/public", http.FileServer(http.Dir("public/"))))
}

func home(res http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" {
		profile(res, req)
		return
	}

	ctx := appengine.NewContext(req)
	u := user.Current(ctx)
	log.Infof(ctx, "user: ", u)
	// pointers can be NIL so don't use a Profile * Profile here:
	var model struct {
		Profile Profile
		Tweets []Tweet
		LoggedIn bool
	}

	model.LoggedIn = checkLoggedInStats(req)

	if u != nil {
		profile, err := getProfileByEmail(ctx, u.Email)
		if err != nil {
		http.Redirect(res, req, "/login", 302)
			return
		}
		model.Profile = *profile
	}

	// TODO: get recent tweets
	var tweets []Tweet
	tweets, err := recentTweets(ctx)
	if err != nil {
		http.Error(res, err.Error(), 500)
	}
	model.Tweets = tweets


	renderTemplate(res, "home.html",  model)
}

func login(res http.ResponseWriter, req *http.Request) {
	ctx := appengine.NewContext(req)
	u := user.Current(ctx)

	// look for the user's profile
	profile, err := getProfileByEmail(ctx, u.Email)
	// if it exists redirect
	if err == nil && profile.Username != "" {
		http.Redirect(res, req, "/"+profile.Username, 302)
		return
	}

	var model struct {
		Profile *Profile
		Error   string
		LoggedIn bool
	}

	model.LoggedIn = checkLoggedInStats(req)
	model.Profile = &Profile{Email: u.Email}

	// create the profile
	username := req.FormValue("username")
	if username != "" {
		_, err = getProfileByUsername(ctx, username)
		// if the username is already taken
		if err == nil {
			model.Error = "username is not available"
			model.Profile.Username = username // caleb didn't have this line
		} else {
			model.Profile.Username = username
			// try to create the profile
			err = createProfile(req, model.Profile)
			if err != nil {
				model.Error = err.Error()
			} else {
				// on success redirect to the user's timeline
				waitForProfile(req, username)
				http.SetCookie(res, &http.Cookie{Name: "logged_in", Value: "true"})
				http.Redirect(res, req, "/"+username, 302)
				return
			}
		}
	}
	// render the login template
	renderTemplate(res, "login.html", model)
}

func profile(res http.ResponseWriter, req *http.Request) {
	ctx := appengine.NewContext(req)
	// get the username
	username := strings.SplitN(req.URL.Path, "/", 2)[1]
	// get the profile
	profile, err := getProfileByUsername(ctx, username)
	if err != nil {
		http.Error(res, err.Error(), 404)
		return
	}

	// get user's tweets
	var tweets []Tweet
	tweets, err = userTweets(ctx, profile.Email)
	if err != nil {
		http.Error(res, err.Error(), 500)
		return
	}

	// Render the template
	var model struct {
		Profile *Profile
		Tweets []Tweet
		LoggedIn bool
	}
	model.LoggedIn = checkLoggedInStats(req)
	model.Profile = profile
	model.Tweets = tweets

	renderTemplate(res, "profile.html", model)
	/*

	// Render the template
	type Model struct {
		Profile *Profile
	}
	renderTemplate(res, "user-profile", Model{
		Profile: profile,
	})

	*/
}

func handleTweet(res http.ResponseWriter, req *http.Request) {
	ctx := appengine.NewContext(req)
	u := user.Current(ctx)
	var tweet *Tweet
	tweet = decodeTweet(ctx, res, req);
	tweet.Time = time.Now()
	// add in username
	var profile *Profile
	profile, err := getProfileByEmail(ctx, u.Email)
	tweet.Username = profile.Username
	// send emails to people mentioned in tweet
	emailMentions(ctx, tweet)
	// post the tweet
	err = putTweet(ctx, tweet, u.Email)
	if err != nil {
		http.Error(res, err.Error(), 500)
		return
	}
	json.NewEncoder(res).Encode(true)
}

func decodeTweet(ctx context.Context, res http.ResponseWriter, req *http.Request) *Tweet {
	var tweet Tweet
	err := json.NewDecoder(req.Body).Decode(&tweet)
	if err != nil {
		log.Errorf(ctx, "error unmarshalling todos: %v", err)
		http.Error(res, err.Error(), 500)
		return nil
	}
	return &tweet
}

func logout(res http.ResponseWriter, req *http.Request) {
	ctx := appengine.NewContext(req)
	http.SetCookie(res, &http.Cookie{Name: "logged_in", Value: "", MaxAge:-1})
	url, err := user.LogoutURL(ctx, "/")
	if err != nil {
		http.Error(res, err.Error(), 500)
		return
	}
	http.Redirect(res, req, url, 302)
}

func emailMentions(ctx context.Context, tweet *Tweet) {
	u := user.Current(ctx)
	var words []string
	words = strings.Fields(tweet.Message)
	for _, value := range words {
		if strings.HasPrefix(value, "@") {
			username := value[1:]
			profile, err := getProfileByUsername(ctx, username)
			if err != nil {
				// they don't have a profile, so skip it
				continue
			}
			msg := &mail.Message{
				Sender:  u.Email,
				To:      []string{profile.Username + " <" + profile.Email +">"},
				Subject: "You were mentioned in a tweet",
				Body:    tweet.Message + " from " + tweet.Username + " - " + humanize.Time(tweet.Time),
			}
			if err := mail.Send(ctx, msg); err != nil {
				log.Errorf(ctx, "Alas, my user, the email failed to sendeth: %v", err)
				continue
			}

		}
	}
}